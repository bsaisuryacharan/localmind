// localmind: CLI entrypoint.
//
// Subcommands:
//   init       detect hardware, write .env + active profile in models.yml
//   up         docker compose up -d (with the right overlays for the host)
//   down       docker compose down
//   status     print container health
//   backup     tar+zstd of all docker volumes
//   restore    restore from a backup archive (destructive)
//   doctor     diagnose common problems
//   profile    benchmark the active model and recommend a profile
//   tunnel     wrap `tailscale funnel` for mobile access
//   keepalive  prevent the host from sleeping while the stack is up
//   responder  host-side HTTP service that wakes the stack on demand
//   agent      multi-agent orchestration (decompose + parallel + chat)
//   version    print the localmind version
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/localmind/localmind/wizard/internal/wizard"
)

// version is set at build time via -ldflags "-X main.version=<tag>".
// Defaults to "dev" for unreleased local builds.
var version = "dev"

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	cmd := os.Args[1]
	args := os.Args[2:]

	switch cmd {
	case "init":
		mustRun(wizard.Init(ctx, args))
	case "up":
		mustRun(wizard.Up(ctx, args))
	case "down":
		mustRun(wizard.Down(ctx, args))
	case "status":
		mustRun(wizard.Status(ctx, args))
	case "backup":
		mustRun(wizard.Backup(ctx, args))
	case "restore":
		mustRun(wizard.Restore(ctx, args))
	case "doctor":
		mustRun(wizard.Doctor(ctx, args))
	case "profile":
		mustRun(wizard.Profile(ctx, args))
	case "tunnel":
		mustRun(wizard.Tunnel(ctx, args))
	case "keepalive":
		mustRun(wizard.Keepalive(ctx, args))
	case "responder":
		mustRun(wizard.Responder(ctx, args))
	case "agent":
		mustRun(wizard.Agent(ctx, args))
	case "-v", "--version", "version":
		fmt.Printf("localmind %s\n", version)
	case "-h", "--help", "help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", cmd)
		usage()
		os.Exit(2)
	}
}

func mustRun(err error) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `localmind: self-hosted AI in a box

usage:
  localmind <command> [args]

commands:
  init        detect hardware and write configuration
  up          start the stack
  down        stop the stack
  status      show container health
  backup      snapshot all data to a tar.zst archive
  restore     restore from a backup archive (destructive)
  doctor      diagnose common problems
  profile     benchmark the active model and recommend a profile
  tunnel      wrap tailscale funnel for mobile access
  keepalive   prevent the host from sleeping while the stack is up
  responder   host-side service that wakes the stack on demand
  agent       multi-agent orchestration (decompose + parallel + chat)
  version     print the localmind version

run 'localmind <command> -h' for command-specific flags.
`)
}
