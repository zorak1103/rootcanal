package sshconn

import (
	"log/slog"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

// keepaliveClient is the interface StartKeepalive needs — a subset of *ssh.Client.
type keepaliveClient interface {
	SendRequest(name string, wantReply bool, payload []byte) (bool, []byte, error)
	Close() error
}

// StartKeepalive starts a background goroutine that sends periodic SSH keepalive requests.
// If interval is 0, no goroutine is started and a no-op stop func is returned.
// If maxFailures is 0, the connection is never closed on keepalive errors (keepalives are still sent).
// If onDead is non-nil, it is called once immediately after client.Close() when max failures are reached.
// The returned stop func is safe to call multiple times.
func StartKeepalive(client *ssh.Client, interval time.Duration, maxFailures int, log *slog.Logger, onDead func()) func() {
	return startKeepalive(client, interval, maxFailures, log, onDead)
}

func startKeepalive(client keepaliveClient, interval time.Duration, maxFailures int, log *slog.Logger, onDead func()) func() {
	if interval == 0 {
		return func() {}
	}
	stop := make(chan struct{})
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		consecutive := 0
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				if keepaliveTick(client, log, onDead, maxFailures, &consecutive) {
					return
				}
			}
		}
	}()
	var once sync.Once
	return func() { once.Do(func() { close(stop) }) }
}

// keepaliveTick sends one keepalive request and updates *consecutive. It
// returns true once maxFailures consecutive failures have been reached and
// the connection has been closed — the caller's loop should then stop.
func keepaliveTick(client keepaliveClient, log *slog.Logger, onDead func(), maxFailures int, consecutive *int) bool {
	_, _, err := client.SendRequest("keepalive@openssh.com", true, nil)
	if err == nil {
		*consecutive = 0
		return false
	}
	*consecutive++
	if log != nil {
		log.Debug("ssh keepalive failed", "consecutive", *consecutive, "err", err)
	}
	if maxFailures <= 0 || *consecutive < maxFailures {
		return false
	}
	if log != nil {
		log.Warn("ssh keepalive: max failures reached, closing connection", "failures", *consecutive)
	}
	_ = client.Close()
	if onDead != nil {
		onDead()
	}
	return true
}
