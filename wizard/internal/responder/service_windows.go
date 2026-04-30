//go:build windows

package responder

import (
	"context"
	"fmt"
	"log"

	"golang.org/x/sys/windows/svc"
)

// serviceName is what we register with the Windows SCM. Must match the
// name used by `sc.exe create` in the wizard package.
const serviceName = "LocalmindResponder"

// RunAsService starts the responder under the Windows Service Control
// Manager. SCM expects the binary to handshake within ~30s of process
// start (we report Running once the HTTP listener has been launched) and
// to react to Stop / Shutdown by transitioning to Stopped.
//
// The flow:
//
//  1. svc.Run blocks the calling goroutine and routes SCM messages into
//     our Execute method.
//  2. Execute starts Server.Run in a child goroutine with a cancellable
//     context, reports Running, then waits on the SCM request channel.
//  3. On Stop / Shutdown, we cancel the context (Server.Run unwinds via
//     http.Server.Shutdown) and report Stopped.
func RunAsService(ctx context.Context, srv *Server) error {
	h := &winHandler{ctx: ctx, srv: srv}
	if err := svc.Run(serviceName, h); err != nil {
		return fmt.Errorf("svc.Run: %w", err)
	}
	return h.runErr
}

type winHandler struct {
	ctx    context.Context
	srv    *Server
	runErr error
}

// Execute is called by golang.org/x/sys/windows/svc once SCM has
// connected. The contract: we MUST send a StartPending status before we
// do any blocking work, send Running once we are accepting requests, and
// send Stopped (or StopPending → Stopped) before returning.
func (h *winHandler) Execute(_ []string, r <-chan svc.ChangeRequest, s chan<- svc.Status) (svcSpecificEC bool, exitCode uint32) {
	const accepts = svc.AcceptStop | svc.AcceptShutdown
	s <- svc.Status{State: svc.StartPending}

	runCtx, cancel := context.WithCancel(h.ctx)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- h.srv.Run(runCtx)
	}()

	s <- svc.Status{State: svc.Running, Accepts: accepts}

	for {
		select {
		case err := <-done:
			// Server exited on its own (probably an ListenAndServe error).
			// Tell SCM we're stopping with a non-zero exit code so it can
			// trigger the configured failure actions (auto-restart).
			h.runErr = err
			if err != nil {
				log.Printf("responder service: server exited: %v", err)
				s <- svc.Status{State: svc.Stopped}
				return false, 1
			}
			s <- svc.Status{State: svc.Stopped}
			return false, 0
		case c := <-r:
			switch c.Cmd {
			case svc.Interrogate:
				s <- c.CurrentStatus
			case svc.Stop, svc.Shutdown:
				s <- svc.Status{State: svc.StopPending}
				cancel()
				// Wait for Server.Run to unwind (it observes ctx.Done()
				// and shuts the http.Server down with a 10s budget).
				h.runErr = <-done
				s <- svc.Status{State: svc.Stopped}
				return false, 0
			}
		}
	}
}
