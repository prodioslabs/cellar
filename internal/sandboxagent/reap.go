package sandboxagent

import (
	"os"
	"os/signal"
	"syscall"
	"time"
)

// ReapZombies periodically reaps orphaned children (PID 1 duty).
func ReapZombies(stop <-chan struct{}) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			for {
				var status syscall.WaitStatus
				pid, err := syscall.Wait4(-1, &status, syscall.WNOHANG, nil)
				if pid <= 0 || err != nil {
					break
				}
			}
		}
	}
}

// NotifyShutdown returns a channel closed on SIGTERM/SIGINT.
func NotifyShutdown() <-chan struct{} {
	ch := make(chan struct{})
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-sigCh
		close(ch)
	}()
	return ch
}
