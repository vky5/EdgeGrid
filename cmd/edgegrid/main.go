package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/joho/godotenv"

	"github.com/edgegrid/edgegrid/internal/agent"
	"github.com/edgegrid/edgegrid/internal/config"
	"github.com/edgegrid/edgegrid/internal/nodeident"
	"github.com/edgegrid/edgegrid/internal/nodelog"
	"github.com/edgegrid/edgegrid/internal/profile"
	"github.com/edgegrid/edgegrid/internal/tui/app"
	tuiclient "github.com/edgegrid/edgegrid/internal/tui/client"
)

func main() {
	_ = godotenv.Load()

	// Determine if running headless agent based on role flags.
	runHeadless := false
	for _, arg := range os.Args {
		if arg == "--server" || arg == "--client" || arg == "-server" || arg == "-client" {
			runHeadless = true
			break
		}
	}

	if len(os.Args) > 1 && !runHeadless {
		subcommand := os.Args[1]
		switch subcommand {
		case "dashboard", "onboard":
			args := append([]string(nil), os.Args[2:]...)
			os.Args = append(os.Args[:1], args...)
			runTUI(args, subcommand)
			return
		case "logs":
			runLogs(os.Args[2:])
			return
		case "profile":
			runProfile(os.Args[2:])
			return
		}
	}

	if !runHeadless {
		// Default to Welcome Screen when no subcommand or role flags are provided.
		args := append([]string(nil), os.Args[1:]...)
		runTUI(args, "welcome")
		return
	}

	cfg := config.LoadConfig()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	nodeAgent, closeLog, err := agent.NewAgentWithLogging(ctx, cfg, nil, false)
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
func runTUI(args []string, startMode string) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	coord := argValue(args, "--coord")
	dir := config.ResolveDataDir(argValue(args, "--data-dir"))

	localAdminToken := nodeident.LoadToken(dir, "admin.token")
	adminToken := argValue(args, "--admin-token")

	// Profile settings (ports, executor) live in dataDir/settings.json.
	bootCfg := config.LoadConfig()
	bootCfg.DataDir = dir
	config.ApplyProfileSettings(bootCfg)

	if coord == "" && adminToken == "" && localAdminToken != "" {
		api := bootCfg.Server.Port
		if api == "" {
			api = ":8080"
		}
		if !strings.HasPrefix(api, ":") {
			api = ":" + api
		}
		coord = "http://127.0.0.1" + api
		adminToken = localAdminToken
	}

	isWorker := localAdminToken == "" && nodeident.LoadToken(dir, "node.token") != ""
	connected := coord != "" && adminToken != ""
	noAgent := hasFlag(args, "--no-agent")

	// If the node is already onboarded and we aren't starting in the welcome screen,
	// start the local agent in the background so the dashboard client can connect to it.
	// The handle is passed into app.New below instead of only living in this
	// goroutine's closure — App is the single place that knows whether a
	// node is already running, so re-onboarding via "/onboard" can replace
	// it instead of silently starting a second one alongside it.
	var runningAgent *agent.Agent
	if (connected || isWorker) && startMode != "welcome" && !noAgent {
		cfg := bootCfg
		if localAdminToken != "" {
			cfg.Server.Enabled = true
			if cfg.JoinURL == "" || nodeident.LoadToken(dir, "node.token") != "" {
				cfg.Client.Enabled = true
			} else {
				cfg.Client.Enabled = false
			}
		} else if isWorker {
			cfg.Server.Enabled = false
			cfg.Client.Enabled = true
			cfg.EmbedNATS = false
		}

		nodeAgent, closeLog, err := agent.NewAgentWithLogging(ctx, cfg, nil, true)
		if err == nil {
			runningAgent = nodeAgent
			go func() {
				_ = nodeAgent.Start(ctx)
				nodeAgent.Close()
				closeLog()
			}()
		}
	}

	var c tuiclient.Client
	if connected {
		c = tuiclient.NewHTTP(coord, adminToken)
	} else {
		c = tuiclient.New()
	}

	a := app.New(ctx, dir, c, coord, connected, isWorker, runningAgent)
	switch startMode {
	case "onboard":
		a = a.StartInOnboarding()
	case "welcome":
		a = a.StartInWelcome()
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

	if newProfile, onboard, noAgentRestart, restart := finalApp.WantsRestart(); restart {
		execRestart(newProfile, onboard, noAgentRestart, args)
		return // only reached if the exec itself failed
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

// execRestart replaces the current process with a fresh `edgegrid dashboard`
// for the newly active profile. Data dir is baked in at startup (NATS,
// tsnet, and the HTTP listeners are already running against the old one),
// so an in-place switch isn't possible — only a real restart is. args is
// stripped of any --data-dir so the new process re-resolves via the profile
// just set instead of the explicit flag winning again and silently undoing
// the switch (see config.ResolveDataDir's precedence).
func execRestart(profileName string, onboard bool, noAgent bool, args []string) {
	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "restart for profile %q failed: %v\n", profileName, err)
		return
	}

	sub := "dashboard"
	if onboard {
		sub = "onboard"
	} else {
		root, err := profile.Root()
		if err == nil {
			dir := filepath.Join(root, profileName)
			if profileName == "" {
				dir = "./data"
			}
			_, err1 := os.Stat(filepath.Join(dir, "admin.token"))
			_, err2 := os.Stat(filepath.Join(dir, "node.token"))
			if err1 != nil && err2 != nil {
				sub = "onboard"
			}
		}
	}

	cleanArgs := stripFlag(args, "--data-dir")
	cleanArgs = stripFlag(cleanArgs, "--no-agent")
	if noAgent {
		cleanArgs = append(cleanArgs, "--no-agent")
	}

	newArgv := append([]string{exe, sub}, cleanArgs...)

	var newEnv []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "DATA_DIR=") {
			newEnv = append(newEnv, kv)
		}
	}

	if err := syscall.Exec(exe, newArgv, newEnv); err != nil {
		fmt.Fprintf(os.Stderr, "restart for profile %q failed: %v\n", profileName, err)
	}
}

func hasFlag(args []string, flag string) bool {
	for _, a := range args {
		if a == flag {
			return true
		}
	}
	return false
}

// stripFlag removes a "--flag value" or "--flag=value" pair from args.
func stripFlag(args []string, flag string) []string {
	var out []string
	for i := 0; i < len(args); i++ {
		if args[i] == flag {
			i++ // also skip the value
			continue
		}
		if strings.HasPrefix(args[i], flag+"=") {
			continue
		}
		out = append(out, args[i])
	}
	return out
}

// runProfile implements `edgegrid profile list|use <name>|current`.
func runProfile(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: edgegrid profile <list|use|current> [name]")
		os.Exit(1)
	}
	switch args[0] {
	case "list":
		names, err := profile.List()
		if err != nil {
			fmt.Fprintf(os.Stderr, "listing profiles: %v\n", err)
			os.Exit(1)
		}
		active := profile.Active()
		for _, n := range names {
			marker := "  "
			if n == active {
				marker = "* "
			}
			fmt.Println(marker + n)
		}
	case "current":
		if a := profile.Active(); a != "" {
			fmt.Println(a)
		} else {
			fmt.Println("(none — using ./data)")
		}
	case "use":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: edgegrid profile use <name>")
			os.Exit(1)
		}
		if err := profile.Use(args[1]); err != nil {
			fmt.Fprintf(os.Stderr, "switching profile: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("active profile set to %q\n", args[1])
	default:
		fmt.Fprintf(os.Stderr, "unknown profile subcommand %q\n", args[0])
		os.Exit(1)
	}
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
