//go:build windows

package wizard

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"golang.org/x/sys/windows"
)

// isElevatedWindows reports whether the current process can create
// services. We test by trying to open the Service Control Manager with
// SC_MANAGER_CREATE_SERVICE: an unprivileged user gets ERROR_ACCESS_DENIED
// while an admin token gets a real handle. This is more reliable than
// shelling out to `net session` or peeking at the user's group SIDs.
func isElevatedWindows() bool {
	h, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CREATE_SERVICE)
	if err != nil {
		return false
	}
	_ = windows.CloseServiceHandle(h)
	return true
}

// windowsServiceInstalled reports whether the LocalmindResponder service
// is registered with Windows. Uses `sc.exe query` because it has no
// extra-handle / privilege requirements.
func windowsServiceInstalled() bool {
	cmd := exec.Command("sc.exe", "query", winServiceName)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return false
	}
	// `sc.exe query <missing>` exits non-zero with "specified service does
	// not exist"; if we got here with err == nil the service is registered.
	_ = out
	return true
}

// windowsRunKeyInstalled reports whether the legacy HKCU\Run value exists.
func windowsRunKeyInstalled() bool {
	cmd := exec.Command("reg", "query", winRunKey, "/v", winRunValueName)
	if err := cmd.Run(); err != nil {
		return false
	}
	return true
}

// installWindowsService registers the responder as a real Windows Service
// via sc.exe. Uses delayed-auto so the service starts after the user-mode
// boot has settled (Docker Desktop in particular doesn't appreciate being
// raced at boot time). The service runs as LocalSystem by default; for
// Docker Desktop CLI access on standard installs that is sufficient.
func installWindowsService(exe, workdir string) error {
	// Note the spaces after `=` in sc.exe arguments — that quirk is
	// required by sc.exe's argument parser. Each "key= value" pair must be
	// passed as TWO separate args so Go's exec doesn't quote them as one.
	binPath := fmt.Sprintf(`"%s" responder run --service`, exe)

	createArgs := []string{
		"create", winServiceName,
		"binPath=", binPath,
		"start=", "delayed-auto",
		"DisplayName=", winServiceDispName,
	}
	if out, err := exec.Command("sc.exe", createArgs...).CombinedOutput(); err != nil {
		return fmt.Errorf("sc.exe create: %w: %s", err, strings.TrimSpace(string(out)))
	}

	// Description is best-effort: a failure here doesn't undo the service.
	if out, err := exec.Command("sc.exe", "description", winServiceName, winServiceDesc).CombinedOutput(); err != nil {
		fmt.Printf("responder: warning: sc.exe description: %v: %s\n", err, strings.TrimSpace(string(out)))
	}

	// Auto-restart on failure: 5s, then 15s, then 60s; reset the failure
	// counter once a day. This matches the resilience profile of the
	// systemd unit on Linux (Restart=on-failure, RestartSec=3).
	failureArgs := []string{
		"failure", winServiceName,
		"reset=", "86400",
		"actions=", "restart/5000/restart/15000/restart/60000",
	}
	if out, err := exec.Command("sc.exe", failureArgs...).CombinedOutput(); err != nil {
		fmt.Printf("responder: warning: sc.exe failure: %v: %s\n", err, strings.TrimSpace(string(out)))
	}

	if out, err := exec.Command("sc.exe", "start", winServiceName).CombinedOutput(); err != nil {
		// Not fatal: a delayed-auto service may also fail to start
		// immediately if dependencies aren't ready. The service is still
		// installed and will start on next boot.
		fmt.Printf("responder: warning: sc.exe start: %v: %s\n", err, strings.TrimSpace(string(out)))
	}

	// workdir is informational here; the service's working directory is
	// %SystemRoot%\System32 by default which is fine because all paths
	// the responder uses are absolute.
	_ = workdir
	fmt.Printf("responder: installed as Windows Service %q (delayed-auto, LocalSystem)\n", winServiceName)
	return nil
}

// uninstallWindowsService stops then deletes the service. Both steps are
// best-effort: stopping a service that's already stopped is harmless and
// `sc.exe delete` succeeds either way.
func uninstallWindowsService() error {
	_ = exec.Command("sc.exe", "stop", winServiceName).Run()
	out, err := exec.Command("sc.exe", "delete", winServiceName).CombinedOutput()
	if err != nil {
		text := strings.TrimSpace(string(out))
		if strings.Contains(strings.ToLower(text), "does not exist") {
			fmt.Println("responder: service not installed")
			return nil
		}
		return fmt.Errorf("sc.exe delete: %w: %s", err, text)
	}
	fmt.Printf("responder: uninstalled service %q\n", winServiceName)
	return nil
}

// statusWindowsService prints the SCM-reported state for the service.
func statusWindowsService(_ context.Context) error {
	out, err := exec.Command("sc.exe", "query", winServiceName).CombinedOutput()
	if err != nil {
		fmt.Println("responder: service not installed")
		return nil
	}
	fmt.Printf("responder: installed as Windows Service %q\n", winServiceName)
	fmt.Print(string(out))
	return nil
}
