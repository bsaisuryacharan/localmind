//go:build !windows

package responder

import (
	"context"
	"errors"
)

// RunAsService is the non-Windows stub. The wizard CLI may still call it
// from cross-platform code paths; on Unix-likes the responder is run by
// launchd / systemd in foreground mode and never via this entry point.
func RunAsService(_ context.Context, _ *Server) error {
	return errors.New("responder: --service is only supported on Windows")
}
