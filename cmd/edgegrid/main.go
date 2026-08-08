package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/joho/godotenv"

	"github.com/edgegrid/edgegrid/internal/node"
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
	fmt.Fprintln(os.Stderr, "usage: edgegrid <up|logs|profile> [args]")
	fmt.Fprintln(os.Stderr, "  up       bring this node onto the tailnet and block until interrupted")
	fmt.Fprintln(os.Stderr, "  logs     tail this node's log file")
	fmt.Fprintln(os.Stderr, "  profile  list | use <name> | current")
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
