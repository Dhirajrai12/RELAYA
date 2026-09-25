//go:build windows

package server

import (
	"context"
	"sync"
	"time"

	"golang.org/x/sys/windows/svc"
)

// asService lets the binary run under the Windows Service Control Manager.
// When started by the SCM, a Stop/Shutdown request cancels ctx, and the
// returned stop function (deferred in main) reports "stopped" only after the
// server has drained. Run from a console, it changes nothing.
func asService(ctx context.Context, stop context.CancelFunc) (context.Context, context.CancelFunc) {
	isService, err := svc.IsWindowsService()
	if err != nil || !isService {
		return ctx, stop
	}
	ctx, cancel := context.WithCancel(ctx)
	h := &handler{cancel: cancel, done: make(chan struct{})}
	finished := make(chan struct{})
	go func() {
		_ = svc.Run("", h) // the name is ignored for own-process services
		cancel()
		close(finished)
	}()

	var once sync.Once
	return ctx, func() {
		once.Do(func() {
			cancel()
			stop()
			close(h.done)
			<-finished
		})
	}
}

type handler struct {
	cancel context.CancelFunc
	done   chan struct{} // closed when main has shut down
}

func (h *handler) Execute(_ []string, reqs <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	status <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	for {
		select {
		case <-h.done: // main exited on its own (e.g. listen error)
			return false, 0
		case c := <-reqs:
			switch c.Cmd {
			case svc.Interrogate:
				status <- c.CurrentStatus
			case svc.Stop, svc.Shutdown:
				status <- svc.Status{State: svc.StopPending, WaitHint: uint32((20 * time.Second).Milliseconds())}
				h.cancel()
				<-h.done
				return false, 0
			}
		}
	}
}
