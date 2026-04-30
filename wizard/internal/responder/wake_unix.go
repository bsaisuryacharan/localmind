//go:build !windows

package responder

import "context"

// WakeDockerDesktop is a no-op on unix platforms. Docker is daemon-style
// on Linux (systemd-managed) and macOS (Docker Desktop on macOS does not
// suffer the same Modern-Standby pause as Windows in practice; the
// existing WakeRunner flow handles startup latency adequately). The
// Windows-specific wake path lives in wake_windows.go.
func WakeDockerDesktop(ctx context.Context) error {
	return nil
}
