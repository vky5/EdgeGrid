package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/joho/godotenv"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/edgegrid/edgegrid/internal/discovery"
	"github.com/edgegrid/edgegrid/internal/node"
	"github.com/edgegrid/edgegrid/internal/tailscaleapi"
	"github.com/edgegrid/edgegrid/internal/task"
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
		case "task":
			runTask(os.Args[2:])
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
	fmt.Fprintln(os.Stderr, "usage: edgegrid <up|dashboard|logs|profile|task> [args]")
	fmt.Fprintln(os.Stderr, "  up         bring this node onto the tailnet and block until interrupted")
	fmt.Fprintln(os.Stderr, "  dashboard  bring this node up and open the terminal dashboard")
	fmt.Fprintln(os.Stderr, "  logs       tail this node's log file")
	fmt.Fprintln(os.Stderr, "  profile    list | use <name> | current")
	fmt.Fprintln(os.Stderr, "  task       send <kind> [input-json] — dispatch a task to the first online peer that accepts it")
}

// joins the tailnet and runs the node until it's stopped, e.g. with Ctrl-C
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

// starts the node and waits until a signal or the node itself stops it
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

// joins the tailnet like runNode, then shows the TUI
// logs go only to the file while the TUI is on screen
func runDashboard() {
	// the welcome screen runs before LoadConfig, so picking a profile still picks the data folder
	// once the node is up, switching profile needs a full restart (execRestart)
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

	// a join key typed on the join screen, used only if --ts-authkey and TS_AUTHKEY are empty
	// it must be set before boot, since tsnet reads the key partway through starting
	if choice.AuthKey != "" && cfg.TailscaleAuthKey == "" {
		cfg.TailscaleAuthKey = choice.AuthKey
	}

	// the first node has nobody to give it a join key, so make one with this profile's Tailscale API login
	// or browser login would join it untagged under a person's account, and other nodes wouldn't see it
	// if making the key fails, fall back to browser login, never stop
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

	// the dashboard needs peer discovery running too, like runForeground does for runNode
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
	// use the profile name read together with DataDir, not node.ActiveProfile() again
	// another edgegrid process may switch profiles in between, and the label would be wrong
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
				Started:   t.Started,
			})
		}
		return out
	}).WithTrust(dashboard.TrustFuncs{List: nodeAgent.TrustedPeers, Set: nodeAgent.SetTrust}).
		WithTasks(dashboard.TaskFuncs{
			ListTrust: nodeAgent.TrustedTaskPeers,
			SetTrust:  nodeAgent.SetTaskTrust,
			Dispatch: func(ctx context.Context, p discovery.Peer, kind string) (string, error) {
				t, err := task.New(kind, task.Requirements{})
				if err != nil {
					return "", err
				}
				_, err = nodeAgent.DispatchTask(ctx, []discovery.Peer{p}, t)
				return t.ID, err
			},
			Await: nodeAgent.AwaitResult,
		}).
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

// restarts as `edgegrid dashboard` after "/profile <name>" switches profile
// the node ID, tailnet session and log file are tied to the old data folder, so only a restart works
// --data-dir is dropped, or it would win again and quietly undo the switch
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

// removes a flag and its value from args, e.g. "--data-dir x" or "--data-dir=x"
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

// runs `edgegrid task send <kind> [input-json]`
func runTask(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: edgegrid task <send>")
		os.Exit(1)
	}
	switch args[0] {
	case "send":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: edgegrid task send <kind> [input-json]")
			os.Exit(1)
		}
		var input json.RawMessage
		if len(args) > 2 {
			if !json.Valid([]byte(args[2])) {
				fmt.Fprintf(os.Stderr, "input is not valid JSON: %s\n", args[2])
				os.Exit(1)
			}
			input = json.RawMessage(args[2])
		}
		runTaskSend(args[1], input)
	default:
		fmt.Fprintf(os.Stderr, "unknown task subcommand %q\n", args[0])
		os.Exit(1)
	}
}

// sends a task to every online peer and waits for each result, for trying it by hand
// it only needs the tailnet, not the listener, since it never receives anything
func runTaskSend(kind string, input json.RawMessage) {
	cfg := node.LoadConfig()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	nodeAgent, closeLog, err := node.NewWithLogging(ctx, cfg, nil, false)
	if err != nil {
		log.Fatalf("failed to initialize EdgeGrid agent: %v", err)
	}
	defer closeLog()
	defer nodeAgent.Close()

	lc, err := nodeAgent.LocalClient()
	if err != nil {
		log.Fatalf("local client: %v", err)
	}

	peers, err := discovery.Snapshot(ctx, lc)
	if err != nil {
		log.Fatalf("peer snapshot: %v", err)
	}

	var online []discovery.Peer
	for _, p := range peers {
		if p.Online {
			online = append(online, p)
		}
	}
	if len(online) == 0 {
		fmt.Println("no online peers to dispatch to")
		return
	}

	t, err := task.New(kind, task.Requirements{})
	if err != nil {
		log.Fatalf("building task: %v", err)
	}
	t.Input = input

	fmt.Printf("dispatching task %s (%s) to %d peer(s)...\n", t.ID, kind, len(online))
	peer, err := nodeAgent.DispatchTask(ctx, online, t)
	if err != nil {
		fmt.Fprintf(os.Stderr, "dispatch failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("accepted by %s, waiting for the result (Ctrl+C stops waiting, not the task)\n", peer.Hostname)

	resp, err := nodeAgent.AwaitResult(ctx, peer, t.ID)
	if err != nil {
		if ctx.Err() != nil {
			err = errors.New("stopped waiting")
		}
		fmt.Fprintf(os.Stderr, "%v: task %s may still be running on %s\n", err, t.ID, peer.Hostname)
		os.Exit(1)
	}
	if err := resp.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", peer.Hostname, err)
		os.Exit(1)
	}
	os.Stdout.Write(resp.Output)
}

// runs `edgegrid profile list|use <name>|current`
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

// prints the end of the log file, e.g. the last 200 lines
// it goes through node.Tail so every log reader agrees on where logs are
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
