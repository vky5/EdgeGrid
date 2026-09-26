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

	"github.com/joho/godotenv"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/edgegrid/edgegrid/internal/node"
	"github.com/edgegrid/edgegrid/internal/tailscaleapi"
	"github.com/edgegrid/edgegrid/internal/tui/app"
	"github.com/edgegrid/edgegrid/internal/tui/dashboard"
)

func main() {
	_ = godotenv.Load()

	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "logs":
			runLogs(os.Args[2:])
			return
		case "profile":
			runProfile(os.Args[2:])
			return
		case "up":
			runNode()
			return
		case "dashboard":
			runDashboard()
			return
		case "-h", "--help", "help":
			usage()
			return
		}
		fmt.Fprintf(os.Stderr, "unknown command %q\n", os.Args[1])
		usage()
		os.Exit(1)
	}

	usage()
	os.Exit(1)
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: edgegrid <up|dashboard|logs|profile> [args]")
	fmt.Fprintln(os.Stderr, "  up         bring this node onto the tailnet and block until interrupted")
	fmt.Fprintln(os.Stderr, "  dashboard  bring this node up and open the terminal dashboard")
	fmt.Fprintln(os.Stderr, "  logs       tail this node's log file")
	fmt.Fprintln(os.Stderr, "  profile    list | use <name> | current")
}

// runNode brings the node up on the tailnet and blocks until interrupted.
func runNode() {
	cfg := node.LoadConfig()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	nodeAgent, closeLog, err := node.NewWithLogging(ctx, cfg, nil, false)
	if err != nil {
		log.Fatalf("failed to initialize EdgeGrid agent: %v", err)
	}
	defer closeLog()

	fmt.Printf("node %s up on %s — ctrl+c to stop\n", nodeAgent.NodeID(), nodeAgent.TailscaleIP())
	runForeground(ctx, nodeAgent)
}

// runForeground starts the agent and blocks until the context is done
// (signal or the agent stopping itself).
func runForeground(ctx context.Context, nodeAgent *node.Node) {
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

// runDashboard brings the node up on the tailnet (same as runNode) and
// hands the terminal to the TUI instead of blocking headlessly. Logs go
// file-only while the TUI owns the screen (node.NewWithLogging's tuiMode)
// — see internal/node/log.go.
func runDashboard() {
	// Welcome runs first, before LoadConfig, so picking a profile still gets
	// to decide which data dir tsnet binds to. Once the node is up that
	// choice costs a full process restart (see execRestart), which is what
	// the old in-dashboard welcome screen had to do on every switch.
	choice, err := app.RunWelcome()
	if err != nil {
		fmt.Fprintf(os.Stderr, "welcome screen: %v\n", err)
		os.Exit(1)
	}
	switch choice.Action {
	case app.WelcomeQuit:
		return
	case app.WelcomeLogs:
		runLogs(nil)
		return
	}

	cfg := node.LoadConfig()

	// An auth key typed on the join screen has to be in place before bring-up:
	// tsnet reads AuthKey inside lb.Start, partway through Up, so there is no
	// way to supply one once boot is under way. An explicit --ts-authkey or
	// TS_AUTHKEY still wins — the screen only fills a gap, it doesn't override
	// what the operator asked for on the command line.
	if choice.AuthKey != "" && cfg.TailscaleAuthKey == "" {
		cfg.TailscaleAuthKey = choice.AuthKey
	}

	// A node starting a brand-new network has no one to hand it a join key —
	// it's the first one. Without one, tsnet falls back to interactive
	// browser login, which registers the device under the operator's
	// personal Tailscale identity with no ACL tag, invisible to every other
	// node's Snapshot() (Tag lives on the device being looked at, not the
	// viewer — see docs/peer-discovery.md). If this profile already has
	// Tailscale API credentials configured (the same ones the Tokens tab
	// uses), mint this node a key from its own credentials and use that
	// instead, so it comes up tagged like every node it will later admit.
	// Best-effort: any failure here just falls back to interactive login,
	// same as if credentials weren't configured at all — never a hard error.
	if cfg.TailscaleAuthKey == "" && !node.HasJoined(cfg.DataDir) {
		if selfClient := tailscaleapi.LoadCredentials(cfg.DataDir); selfClient != nil {
			minted, err := selfClient.CreateKey()
			if err != nil {
				log.Printf("warning: could not mint this node a self-key, falling back to interactive login: %v", err)
			} else {
				cfg.TailscaleAuthKey = minted.Key
			}
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	nodeAgent, closeLog, err := app.RunBoot(ctx, cfg)
	if err != nil {
		log.Fatalf("failed to initialize EdgeGrid node: %v", err)
	}
	if nodeAgent == nil {
		return // cancelled from the boot screen
	}
	if closeLog != nil {
		defer closeLog()
	}
	defer nodeAgent.Close()

	// Peer discovery (listen + dial known peers) has to actually run for the
	// dashboard too, not just headless mode — runForeground does this for
	// runNode, but the dashboard never went through that path.
	go func() {
		if err := nodeAgent.Start(ctx); err != nil {
			log.Printf("discovery: %v", err)
		}
	}()

	tsClient := tailscaleapi.LoadCredentials(cfg.DataDir)
	lc, err := nodeAgent.LocalClient()
	if err != nil {
		log.Printf("warning: tsnet local client unavailable, Peers tab will show an error: %v", err)
	}
	// cfg.ProfileName was resolved atomically with cfg.DataDir inside
	// LoadConfig — re-reading node.ActiveProfile() here separately would be
	// a real race: another EdgeGrid process switching profiles in the gap
	// between that boot-time read and this one would leave DataDir correct
	// but this label wrong (see node.resolveDataDir's doc comment).
	a := app.New(nodeAgent.NodeID(), nodeAgent.TailscaleIP(), cfg.DataDir, cfg.ProfileName, nodeAgent.TailscaleHostname(), tsClient, lc, nodeAgent.SendBlob, func() []dashboard.Transfer {
		live := nodeAgent.Transfers()
		out := make([]dashboard.Transfer, 0, len(live))
		for _, t := range live {
			out = append(out, dashboard.Transfer{
				Direction: string(t.Direction),
				Peer:      t.Peer,
				Frac:      t.Progress.Frac(),
				BytesDone: t.Progress.BytesDone,
				Total:     t.Progress.BytesTotal,
				Verifying: t.Progress.Verifying,
				Done:      t.Done,
				Err:       t.Err,
			})
		}
		return out
	}).WithTrust(dashboard.TrustFuncs{List: nodeAgent.TrustedPeers, Set: nodeAgent.SetTrust}).
		WithHistory(dashboard.HistoryFuncs{Totals: nodeAgent.HistoryTotals, Recent: nodeAgent.RecentTransfers})

	p := tea.NewProgram(a, tea.WithAltScreen())
	finalModel, err := p.Run()
	if err != nil {
		fmt.Fprintf(os.Stderr, "tui error: %v\n", err)
		os.Exit(1)
	}

	if finalApp, ok := finalModel.(app.App); ok {
		if profileName, restart := finalApp.WantsRestart(); restart {
			execRestart(profileName)
			return // only reached if the exec itself failed
		}
	}
}

// execRestart re-execs the process into `edgegrid dashboard` after
// "/profile <name>" switches the active profile — the node identity,
// tsnet session, and log file are all already bound to the old data dir,
// so an in-place switch isn't possible, only a real restart is. args is
// stripped of --data-dir so the new process re-resolves via the profile
// just set instead of the explicit flag winning again and silently
// undoing the switch (see node.ResolveDataDir's precedence).
func execRestart(profileName string) {
	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "restart for profile %q failed: %v\n", profileName, err)
		return
	}

	cleanArgs := stripFlag(os.Args[2:], "--data-dir")
	newArgv := append([]string{exe, "dashboard"}, cleanArgs...)

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

// stripFlag removes a "--flag value" or "--flag=value" pair from args.
func stripFlag(args []string, flag string) []string {
	var out []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == flag {
			i++ // skip its value
			continue
		}
		if strings.HasPrefix(a, flag+"=") {
			continue
		}
		out = append(out, a)
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
		names, err := node.ListProfiles()
		if err != nil {
			fmt.Fprintf(os.Stderr, "listing profiles: %v\n", err)
			os.Exit(1)
		}
		active := node.ActiveProfile()
		for _, n := range names {
			marker := "  "
			if n == active {
				marker = "* "
			}
			fmt.Println(marker + n)
		}
	case "current":
		if a := node.ActiveProfile(); a != "" {
			fmt.Println(a)
		} else {
			fmt.Println("(none — using ./data)")
		}
	case "use":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: edgegrid profile use <name>")
			os.Exit(1)
		}
		if err := node.UseProfile(args[1]); err != nil {
			fmt.Fprintf(os.Stderr, "switching profile: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("active profile set to %q\n", args[1])
	default:
		fmt.Fprintf(os.Stderr, "unknown profile subcommand %q\n", args[0])
		os.Exit(1)
	}
}

// runLogs reads through node.Tail so every log reader agrees about where
// logs live and what they show.
func runLogs(args []string) {
	fs := flag.NewFlagSet("logs", flag.ExitOnError)
	dataDir := fs.String("data-dir", "", "directory for node identity and log files (default ./data)")
	n := fs.Int("n", 200, "number of lines to show")
	_ = fs.Parse(args)

	out, err := node.TailLog(node.ResolveDataDir(*dataDir), *n)
	if err != nil {
		fmt.Fprintf(os.Stderr, "reading logs: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(out)
}
