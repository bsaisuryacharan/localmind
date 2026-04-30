package wizard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Tunnel exposes Open WebUI to other devices the user owns (and, opt-in,
// to the public internet).
//
// Subcommands:
//
//	localmind tunnel join              bring this host onto the user's tailnet
//	                                   (peer-to-peer WireGuard, end-to-end encrypted —
//	                                   recommended default)
//	localmind tunnel funnel [port]     expose Open WebUI on a public HTTPS URL via
//	                                   Tailscale Funnel. TLS terminates at Tailscale's
//	                                   edge, so this is less private than `join`.
//	localmind tunnel start [port]      DEPRECATED alias for `funnel`.
//	localmind tunnel status            print whether funnel is up + the URL
//	localmind tunnel stop              disable funnel
//
// Tailscale itself must already be installed and the user logged in.
// The CLI prints platform-specific install hints if not.
func Tunnel(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return tunnelUsage()
	}
	if err := requireTailscale(); err != nil {
		// `join` is the one subcommand that can recover from a not-logged-in
		// state by running `tailscale up`. For everything else we bail early.
		if args[0] != "join" {
			return err
		}
	}

	sub := args[0]
	rest := args[1:]
	switch sub {
	case "join":
		return tunnelJoin(ctx)
	case "funnel":
		return tunnelFunnel(ctx, rest)
	case "start":
		fmt.Fprintln(os.Stderr, "`localmind tunnel start` is deprecated; use `localmind tunnel funnel`")
		return tunnelFunnel(ctx, rest)
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
	return fmt.Errorf("usage: localmind tunnel {join | funnel [port] | status | stop}")
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

// tsStatus mirrors the subset of `tailscale status --self --json` output we
// care about. The Tailscale `Self` shape has many more fields; we only decode
// what we display.
type tsStatus struct {
	Self struct {
		HostName     string
		DNSName      string
		TailscaleIPs []string
	}
}

// tunnelJoin brings the local host onto the user's tailnet (peer-to-peer,
// end-to-end WireGuard) and prints how to reach Open WebUI from a phone or
// tablet on the same tailnet. Unlike `funnel`, traffic never traverses
// Tailscale's edge in cleartext.
func tunnelJoin(ctx context.Context) error {
	bin, err := exec.LookPath("tailscale")
	if err != nil {
		return errors.New(`tailscale CLI not found on PATH.

Install:
  macOS:    brew install --cask tailscale  (or download from tailscale.com/download)
  Linux:    curl -fsSL https://tailscale.com/install.sh | sh
  Windows:  winget install tailscale.tailscale

Then retry: localmind tunnel join`)
	}

	// Probe current state. If logged out / NeedsLogin we run `tailscale up`
	// and forward the login URL to the user.
	probe, probeErr := exec.CommandContext(ctx, bin, "status", "--peers=false", "--self=true").CombinedOutput()
	probeText := string(probe)
	needsLogin := probeErr != nil ||
		strings.Contains(probeText, "Logged out") ||
		strings.Contains(probeText, "NeedsLogin")

	if needsLogin {
		fmt.Println("tunnel join: bringing this host onto the tailnet...")
		up := exec.CommandContext(ctx, bin, "up", "--hostname=localmind-laptop")
		up.Stdout = os.Stdout
		// `tailscale up` prints the login URL on stderr — forward it so the
		// user can click through.
		up.Stderr = os.Stderr
		if err := up.Run(); err != nil {
			return fmt.Errorf("tailscale up: %w (visit the URL above to authenticate, then retry)", err)
		}
	}

	// Re-query, this time as JSON, to grab hostname / MagicDNS / IPs.
	jsonOut, err := exec.CommandContext(ctx, bin, "status", "--self", "--json").Output()
	if err != nil {
		return fmt.Errorf("tailscale status --json: %w", err)
	}
	var st tsStatus
	if err := json.Unmarshal(jsonOut, &st); err != nil {
		return fmt.Errorf("parse tailscale status json: %w", err)
	}

	hostname := strings.TrimSuffix(st.Self.DNSName, ".")
	if hostname == "" {
		hostname = st.Self.HostName
	}
	primaryIP := pickPrimaryIP(st.Self.TailscaleIPs)

	fmt.Println()
	fmt.Println("tunnel join: this host is on your tailnet.")
	fmt.Printf("  hostname (MagicDNS): %s\n", hostname)
	fmt.Printf("  short hostname:      %s\n", st.Self.HostName)
	if primaryIP != "" {
		fmt.Printf("  tailnet IP:          %s\n", primaryIP)
	}
	if len(st.Self.TailscaleIPs) > 1 {
		fmt.Printf("  other IPs:           %s\n", strings.Join(st.Self.TailscaleIPs, ", "))
	}

	reachHost := hostname
	if reachHost == "" {
		reachHost = primaryIP
	}

	fmt.Println()
	fmt.Println("Phone setup:")
	fmt.Println("  1. Install Tailscale: App Store / Play Store")
	fmt.Println("  2. Sign in with the same account")
	fmt.Printf("  3. Open: http://%s:7900   (or :3000 for Open WebUI directly)\n", reachHost)
	fmt.Println()
	fmt.Println("Privacy: traffic is end-to-end WireGuard. Tailscale's coordination")
	fmt.Println("server only sees public keys, never traffic content. For the")
	fmt.Println("strictest \"no third party at all\" setup, see docs/headscale.md.")
	return nil
}

// pickPrimaryIP prefers the IPv4 tailnet address (no colons) and falls back
// to whatever's first (typically IPv6).
func pickPrimaryIP(ips []string) string {
	for _, ip := range ips {
		if !strings.Contains(ip, ":") {
			return ip
		}
	}
	if len(ips) > 0 {
		return ips[0]
	}
	return ""
}

func tunnelFunnel(ctx context.Context, args []string) error {
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
		fmt.Println("tunnel: not active. start with `localmind tunnel funnel`.")
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
