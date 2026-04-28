package wizard

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Tunnel wraps `tailscale funnel` so the user can reach the Open WebUI
// from any device, including their phone, without configuring port
// forwarding or owning a public IP.
//
// Subcommands:
//
//	localmind tunnel start [port]   default port 3000 (the Open WebUI)
//	localmind tunnel status         print whether funnel is up + the URL
//	localmind tunnel stop           disable funnel
//
// Tailscale itself must already be installed and the user logged in.
// The CLI prints platform-specific install hints if not.
func Tunnel(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return tunnelUsage()
	}
	if err := requireTailscale(); err != nil {
		return err
	}

	sub := args[0]
	rest := args[1:]
	switch sub {
	case "start":
		return tunnelStart(ctx, rest)
	case "status":
		return tunnelStatus(ctx)
	case "stop":
		return tunnelStop(ctx)
	case "-h", "--help", "help":
		return tunnelUsage()
	default:
		return fmt.Errorf("unknown tunnel subcommand: %s", sub)
	}
}

func tunnelUsage() error {
	return fmt.Errorf("usage: localmind tunnel {start [port] | status | stop}")
}

// requireTailscale checks that the tailscale CLI is on PATH and the user
// is logged in. Returns a friendly error with install hints if not.
func requireTailscale() error {
	bin, err := exec.LookPath("tailscale")
	if err != nil {
		return errors.New(`tailscale CLI not found on PATH.

Install:
  macOS:    brew install --cask tailscale  (or download from tailscale.com/download)
  Linux:    curl -fsSL https://tailscale.com/install.sh | sh
  Windows:  winget install tailscale.tailscale

Then run: tailscale up
And: tailscale funnel check  (enables Funnel for your tailnet)

After that, retry: localmind tunnel start`)
	}
	out, err := exec.Command(bin, "status", "--peers=false", "--self=true").CombinedOutput()
	if err != nil {
		return fmt.Errorf("tailscale not logged in (`%s status` failed: %s)\nrun: tailscale up",
			bin, strings.TrimSpace(string(out)))
	}
	return nil
}

func tunnelStart(ctx context.Context, args []string) error {
	port := "3000"
	if len(args) > 0 {
		port = args[0]
	}
	cmd := exec.CommandContext(ctx, "tailscale", "funnel", "--bg", port)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("tailscale funnel: %w (note: Funnel must be enabled in the admin console)", err)
	}
	return tunnelStatus(ctx)
}

func tunnelStatus(ctx context.Context) error {
	out, err := exec.CommandContext(ctx, "tailscale", "funnel", "status").CombinedOutput()
	if err != nil {
		return fmt.Errorf("tailscale funnel status: %w: %s", err, strings.TrimSpace(string(out)))
	}
	text := strings.TrimSpace(string(out))
	if text == "" || strings.Contains(text, "No serve config") {
		fmt.Println("tunnel: not active. start with `localmind tunnel start`.")
		return nil
	}
	fmt.Println(text)
	return nil
}

func tunnelStop(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, "tailscale", "funnel", "--bg", "off")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		// `funnel off` returns non-zero if nothing was running. Treat that
		// as success.
		fmt.Printf("tunnel stop: %v (probably already off)\n", err)
		return nil
	}
	fmt.Println("tunnel: stopped")
	return nil
}
