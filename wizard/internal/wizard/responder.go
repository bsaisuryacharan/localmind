package wizard

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/localmind/localmind/wizard/internal/responder"
)

// Responder is the entry point for `localmind responder ...`.
//
// Subcommands:
//
//	run                 foreground HTTP server (used by the OS service unit)
//	install             register the responder to start at user login
//	uninstall           remove the OS service unit
//	status              report whether the unit is installed and running
//
// The responder is a host-side process (not in docker). It exists so a
// phone hitting a stable URL can wake the docker stack on demand. See
// docs/mobile.md for the full architecture.
func Responder(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return responderUsage()
	}
	switch args[0] {
	case "run":
		return responderRun(ctx)
	case "install":
		return responderInstall(ctx)
	case "uninstall":
		return responderUninstall(ctx)
	case "status":
		return responderStatus(ctx)
	case "-h", "--help", "help":
		return responderUsage()
	}
	return fmt.Errorf("unknown responder subcommand: %s", args[0])
}

func responderUsage() error {
	return fmt.Errorf("usage: localmind responder {run | install | uninstall | status}")
}

// responderRun blocks running the HTTP server. Wired so that POST /wake
// triggers a `localmind up --no-profile` via the wizard.Up code path.
func responderRun(ctx context.Context) error {
	cfg := responder.Config{
		// Optional bearer token. If unset, the responder runs unauthenticated
		// (historical behavior). If set, /status, /wake, and the HTML page
		// all require the token; /healthz stays open for monitoring.
		Token: os.Getenv("LOCALMIND_RESPONDER_TOKEN"),
		WakeRunner: func(c context.Context) error {
			// The wake call is best-effort; profile would block much longer
			// than the wake budget so we always skip it here.
			return Up(c, []string{"--no-profile"})
		},
	}
	// Windows needs more headroom on /wake because Docker Desktop has to
	// be unpaused (after Modern Standby resume) before `docker compose up`
	// can succeed. Setting this here is belt-and-suspenders with the
	// matching default in responder.New(); explicit at the wiring site
	// makes the intent obvious to anyone reading responderRun.
	if runtime.GOOS == "windows" {
		cfg.WakeTimeout = 90 * time.Second
	}
	srv := responder.New(cfg)
	return srv.Run(ctx)
}

// --- install / uninstall / status -------------------------------------------

const (
	macServiceLabel = "dev.localmind.responder"
	linuxUnitName   = "localmind-responder.service"
	winRunValueName = "LocalmindResponder"
	winRunKey       = `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`
)

func responderInstall(ctx context.Context) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate self: %w", err)
	}
	exe, err = filepath.Abs(exe)
	if err != nil {
		return fmt.Errorf("abs self path: %w", err)
	}

	root, err := repoRoot()
	if err != nil {
		return err
	}

	switch runtime.GOOS {
	case "darwin":
		return installLaunchd(exe, root)
	case "linux":
		return installSystemdUser(exe, root)
	case "windows":
		return installWindowsRun(ctx, exe, root)
	}
	return fmt.Errorf("responder install: unsupported OS %s", runtime.GOOS)
}

func responderUninstall(ctx context.Context) error {
	switch runtime.GOOS {
	case "darwin":
		return uninstallLaunchd()
	case "linux":
		return uninstallSystemdUser()
	case "windows":
		return uninstallWindowsRun(ctx)
	}
	return fmt.Errorf("responder uninstall: unsupported OS %s", runtime.GOOS)
}

func responderStatus(ctx context.Context) error {
	switch runtime.GOOS {
	case "darwin":
		return statusLaunchd(ctx)
	case "linux":
		return statusSystemdUser(ctx)
	case "windows":
		return statusWindowsRun(ctx)
	}
	return fmt.Errorf("responder status: unsupported OS %s", runtime.GOOS)
}

// --- macOS launchd ---------------------------------------------------------

func launchdPlistPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "LaunchAgents", macServiceLabel+".plist"), nil
}

func installLaunchd(exe, workdir string) error {
	plistPath, err := launchdPlistPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(plistPath), 0o755); err != nil {
		return err
	}
	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>%s</string>
  <key>ProgramArguments</key>
  <array>
    <string>%s</string>
    <string>responder</string>
    <string>run</string>
  </array>
  <key>WorkingDirectory</key><string>%s</string>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>StandardOutPath</key><string>%s/.localmind/responder.log</string>
  <key>StandardErrorPath</key><string>%s/.localmind/responder.log</string>
</dict>
</plist>
`, macServiceLabel, exe, workdir, workdir, workdir)

	if err := os.WriteFile(plistPath, []byte(plist), 0o644); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(workdir, ".localmind"), 0o755); err != nil {
		return err
	}
	// `launchctl load` is the legacy verb; bootstrap is preferred but harder
	// to undo. load works on every macOS version localmind targets.
	if err := exec.Command("launchctl", "unload", plistPath).Run(); err != nil {
		// ignore: not loaded yet
	}
	if err := exec.Command("launchctl", "load", plistPath).Run(); err != nil {
		return fmt.Errorf("launchctl load: %w", err)
	}
	fmt.Printf("responder: installed at %s and started\n", plistPath)
	return nil
}

func uninstallLaunchd() error {
	plistPath, err := launchdPlistPath()
	if err != nil {
		return err
	}
	if _, err := os.Stat(plistPath); err == nil {
		_ = exec.Command("launchctl", "unload", plistPath).Run()
		_ = os.Remove(plistPath)
		fmt.Printf("responder: removed %s\n", plistPath)
		return nil
	}
	fmt.Println("responder: not installed")
	return nil
}

func statusLaunchd(_ context.Context) error {
	plistPath, err := launchdPlistPath()
	if err != nil {
		return err
	}
	if _, err := os.Stat(plistPath); err != nil {
		fmt.Println("responder: not installed")
		return nil
	}
	out, _ := exec.Command("launchctl", "list", macServiceLabel).CombinedOutput()
	text := strings.TrimSpace(string(out))
	if text == "" {
		fmt.Printf("responder: installed (%s) but not loaded\n", plistPath)
		return nil
	}
	fmt.Printf("responder: installed and loaded\n%s\n", text)
	return nil
}

// --- Linux systemd user unit -----------------------------------------------

func systemdUnitPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "systemd", "user", linuxUnitName), nil
}

func installSystemdUser(exe, workdir string) error {
	unitPath, err := systemdUnitPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(unitPath), 0o755); err != nil {
		return err
	}
	unit := fmt.Sprintf(`[Unit]
Description=localmind responder (wakes docker stack on demand)
After=network-online.target

[Service]
Type=simple
WorkingDirectory=%s
ExecStart=%s responder run
Restart=on-failure
RestartSec=3

[Install]
WantedBy=default.target
`, workdir, exe)

	if err := os.WriteFile(unitPath, []byte(unit), 0o644); err != nil {
		return err
	}

	for _, args := range [][]string{
		{"--user", "daemon-reload"},
		{"--user", "enable", "--now", linuxUnitName},
	} {
		cmd := exec.Command("systemctl", args...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("systemctl %s: %w", strings.Join(args, " "), err)
		}
	}
	fmt.Printf("responder: installed at %s and started\n", unitPath)
	fmt.Println("hint: `loginctl enable-linger $USER` to keep it running after logout")
	return nil
}

func uninstallSystemdUser() error {
	unitPath, err := systemdUnitPath()
	if err != nil {
		return err
	}
	_ = exec.Command("systemctl", "--user", "disable", "--now", linuxUnitName).Run()
	_ = os.Remove(unitPath)
	_ = exec.Command("systemctl", "--user", "daemon-reload").Run()
	fmt.Println("responder: uninstalled")
	return nil
}

func statusSystemdUser(_ context.Context) error {
	out, _ := exec.Command("systemctl", "--user", "is-active", linuxUnitName).CombinedOutput()
	state := strings.TrimSpace(string(out))
	if state == "" {
		state = "(unknown)"
	}
	fmt.Printf("responder: %s\n", state)
	return nil
}

// --- Windows registry Run key ----------------------------------------------
//
// We use HKCU\...\Run so install doesn't require admin. Tradeoff: the
// responder only runs while the user is logged in. A proper Windows
// service install (sc.exe create) is a v2 follow-up.

func installWindowsRun(_ context.Context, exe, workdir string) error {
	// Quote the exe path in case it contains spaces. cmd.exe /c sequences
	// the cd then the run so the working directory is right.
	value := fmt.Sprintf(`cmd.exe /c cd /d "%s" && "%s" responder run`, workdir, exe)
	cmd := exec.Command("reg", "add", winRunKey, "/v", winRunValueName,
		"/t", "REG_SZ", "/d", value, "/f")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("reg add: %w: %s", err, strings.TrimSpace(string(out)))
	}
	// Also start it now so the user doesn't have to log out + back in.
	start := exec.Command("cmd.exe", "/c", "start", "/min", "", exe, "responder", "run")
	start.Dir = workdir
	if err := start.Start(); err != nil {
		return fmt.Errorf("start now: %w", err)
	}
	fmt.Printf("responder: registered at %s\\%s and started\n", winRunKey, winRunValueName)
	return nil
}

func uninstallWindowsRun(_ context.Context) error {
	cmd := exec.Command("reg", "delete", winRunKey, "/v", winRunValueName, "/f")
	if out, err := cmd.CombinedOutput(); err != nil {
		text := strings.TrimSpace(string(out))
		if strings.Contains(strings.ToLower(text), "unable to find") {
			fmt.Println("responder: not registered")
			return nil
		}
		return fmt.Errorf("reg delete: %w: %s", err, text)
	}
	fmt.Println("responder: uninstalled (already-running process not killed; reboot or kill manually)")
	return nil
}

func statusWindowsRun(_ context.Context) error {
	out, err := exec.Command("reg", "query", winRunKey, "/v", winRunValueName).CombinedOutput()
	if err != nil {
		fmt.Println("responder: not registered")
		return nil
	}
	fmt.Printf("responder: registered at %s\n", winRunKey)
	fmt.Print(string(out))
	return nil
}
