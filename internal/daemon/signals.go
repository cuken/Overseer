package daemon

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
)

// SignalHandler manages graceful shutdown signals
type SignalHandler struct {
	signals chan os.Signal
	cancel  context.CancelFunc
}

// NewSignalHandler creates a new signal handler
func NewSignalHandler() *SignalHandler {
	return &SignalHandler{
		signals: make(chan os.Signal, 1),
	}
}

// Setup configures signal handling and returns a context that will be
// cancelled when a shutdown signal is received
func (h *SignalHandler) Setup(parent context.Context) context.Context {
	ctx, cancel := context.WithCancel(parent)
	h.cancel = cancel

	signal.Notify(h.signals, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)

	go func() {
		for sig := range h.signals {
			switch sig {
			case syscall.SIGINT, syscall.SIGTERM:
				log.Printf("[Signal] Received %s, initiating shutdown...", sig)
				cancel()
				return
			case syscall.SIGHUP:
				log.Printf("[Signal] Received SIGHUP, reloading configuration...")
				// TODO: Implement config reload
			}
		}
	}()

	return ctx
}

// Stop stops the signal handler
func (h *SignalHandler) Stop() {
	signal.Stop(h.signals)
	close(h.signals)
}
