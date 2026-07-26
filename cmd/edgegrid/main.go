package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/joho/godotenv"

	"github.com/edgegrid/edgegrid/internal/agent"
	"github.com/edgegrid/edgegrid/internal/config"
	"github.com/edgegrid/edgegrid/internal/nodeident"
	"github.com/edgegrid/edgegrid/internal/nodelog"
	"github.com/edgegrid/edgegrid/internal/tui/app"
	tuiclient "github.com/edgegrid/edgegrid/internal/tui/client"
)

func main() {
	if err := godotenv.Load(); err != nil {
		log.Println("no .env file found; using environment variables")
	}

	// Subcommands don't touch the existing headless node-runtime path below.
	if len(os.Args) > 1 {
		subcommand := os.Args[1]
		switch subcommand {
		case "dashboard", "onboard":
			// Strip the subcommand word from the *global* os.Args, not
			// just the local slice handed to runTUI — config.LoadConfig()
			// gets called later (inside the wizard) and does its own
			// flag.Parse() against the global os.Args. The flag package
			// stops parsing at the first non-flag argument, and "onboard"/
			// "dashboard" (not starting with "-") is exactly that sitting
			// right before any real flags — so without this, nothing
			// config.LoadConfig() reads via flags (--server, --client,
			// --replicas, etc.) ever actually gets parsed when running
			// through here.
			// Real copy: the append below reuses os.Args's backing array,
			// which would otherwise clobber args's own contents in place.
			args := append([]string(nil), os.Args[2:]...)
			os.Args = append(os.Args[:1], args...)
			runTUI(args, subcommand == "onboard")
			return
		case "logs":
			runLogs(os.Args[2:])
			return
		}
	}

	cfg := config.LoadConfig()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	nodeAgent, closeLog, err := agent.NewAgentWithLogging(ctx, cfg, nil)
	if err != nil {
		log.Fatalf("failed to initialize EdgeGrid agent: %v", err)
	}
	defer closeLog()
	runForeground(ctx, nodeAgent)
}

// runForeground starts the agent and blocks until the context is done
// (signal or the agent stopping itself) — shared by the plain headless
// path and the post-wizard primary-coordinator path.
func runForeground(ctx context.Context, nodeAgent *agent.Agent) {
	defer nodeAgent.Close()

	stopped := make(chan error, 1)
	go func() { stopped <- nodeAgent.Start(ctx) }()

	select {
	case err := <-stopped:
		if err != nil {
			log.Printf("EdgeGrid agent stopped: %v", err)
		}
	case <-ctx.Done():
		log.Println("received shutdown signal")
	}
}

// runTUI launches the one unified TUI program (internal/tui/app). Both
// `edgegrid dashboard` and `edgegrid onboard` land here — they're the same
// program, just a different starting screen, so "/onboard" from within the
// dashboard and running `edgegrid onboard` directly end up in the exact
// same place. Whichever role the wizard completes with, it already fully
// built the real Agent in the background (see onboarding.startNode) —
// this just takes over running it in the foreground once the TUI exits.
func runTUI(args []string, startInOnboarding bool) {
	// Deliberately not a flag.FlagSet: onboarding's eventual
	// config.LoadConfig() call (inside the wizard, once primary-coordinator
	// is confirmed) accepts many more flags than just these two, and a
	// narrow FlagSet here would abort on any of those before ever reaching
	// it — same reasoning as runLogs not needing this, but runTUI does
	// since it's shared with the onboard path.
	coord := argValue(args, "--coord")
	dir := config.ResolveDataDir(argValue(args, "--data-dir"))

	// The admin token belongs to whichever coordinator we're connecting to
	// — it's issued by that server, not something a client can supply on
	// its own. The local admin.token file is only ever the RIGHT token for
	// one specific case: no --coord was given either, meaning we're
	// defaulting to the coordinator running right here on this same
	// machine/data-dir, which is the one that generated it. If --coord was
	// given explicitly (a real remote address), the local file has nothing
	// to do with that server and must never be substituted in — an
	// explicit --admin-token (or /connect, later) is required instead.
	localAdminToken := nodeident.LoadToken(dir, "admin.token")
	adminToken := argValue(args, "--admin-token")
	if coord == "" && adminToken == "" && localAdminToken != "" {
		coord = "http://127.0.0.1:8080"
		adminToken = localAdminToken
	}

	// A worker's data dir has a node token but never an admin token (only
	// buildCoordinator ever generates one) — distinguishes "this machine
	// simply isn't a coordinator" from "hasn't connected to one yet."
	isWorker := localAdminToken == "" && nodeident.LoadToken(dir, "node.token") != ""

	connected := coord != "" && adminToken != ""
	var c tuiclient.Client
	if connected {
		c = tuiclient.NewHTTP(coord, adminToken)
	} else {
		c = tuiclient.New()
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	a := app.New(ctx, dir, c, coord, connected, isWorker)
	if startInOnboarding {
		a = a.StartInOnboarding()
	}

	final, err := tea.NewProgram(a, tea.WithAltScreen()).Run()
	if err != nil {
		fmt.Fprintf(os.Stderr, "TUI exited with error: %v\n", err)
		os.Exit(1)
	}

	finalApp, ok := final.(app.App)
	if !ok {
		return
	}
	_, confirmed, nodeAgent, _, startErr := finalApp.WizardResult()
	if !confirmed {
		return
	}
	if startErr != nil {
		fmt.Fprintf(os.Stderr, "failed to start node: %v\n", startErr)
		os.Exit(1)
	}

	fmt.Println("node started — ctrl+c to stop")
	runForeground(ctx, nodeAgent)
}

// runLogs is the plain-CLI equivalent of the TUI's /logs command — both
// read through nodelog.Tail, so they never disagree about where logs live
// or what they show.
func runLogs(args []string) {
	fs := flag.NewFlagSet("logs", flag.ExitOnError)
	dataDir := fs.String("data-dir", "", "directory for node identity and log files (default ./data)")
	n := fs.Int("n", 200, "number of lines to show")
	_ = fs.Parse(args)

	out, err := nodelog.Tail(config.ResolveDataDir(*dataDir), *n)
	if err != nil {
		fmt.Fprintf(os.Stderr, "reading logs: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(out)
}

// argValue does a minimal manual scan for "--flag value" or "--flag=value"
// — used instead of the flag package where an unrelated, unregistered flag
// (bound for a different parse pass later) must not abort the process.
func argValue(args []string, flag string) string {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1]
		}
		if v, ok := strings.CutPrefix(a, flag+"="); ok {
			return v
		}
	}
	return ""
}
