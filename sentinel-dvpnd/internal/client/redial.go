package client

import (
	"context"
	"errors"
	"strings"
	"syscall"
	"time"
)

// ─── Redials: a lost network is waited for, not spent ───

const (
	// networkWait caps the time redials wait for the network to come back (airplane mode, Wi-Fi
	// switching, a tunnel through a dead link) before failures count against the attempts again.
	networkWait = 120 * time.Second
	// networkPoll is how often a redial is tried while there is no network.
	networkPoll = 2 * time.Second
)

// networkDown reports a failure that says this host has no network at all, so nothing about the node
// can be learnt from it: no route to any address (ENETUNREACH; Windows: WSAENETUNREACH). A dead or
// refusing node gives other errors (timeout, refused, host unreachable), which still count.
func networkDown(err error) bool {
	if err == nil {
		return false
	}

	if errors.Is(err, syscall.ENETUNREACH) {
		return true
	}

	// Errors that cross an RPC or HTTP client often arrive flattened to text.
	msg := err.Error()

	return strings.Contains(msg, "network is unreachable") || strings.Contains(msg, "unreachable network")
}

// redialPolicy decides when a lost session is dialled again and when the daemon gives up.
type redialPolicy struct {
	attempts uint
	backoff  time.Duration
	netWait  time.Duration
	netPoll  time.Duration
	now      func() time.Time
	// sleep waits d or until wake fires; false when ctx ended.
	sleep func(ctx context.Context, d time.Duration, wake <-chan struct{}) bool
}

func newRedialPolicy(attempts uint, backoff time.Duration) redialPolicy {
	return redialPolicy{
		attempts: attempts, backoff: backoff, netWait: networkWait, netPoll: networkPoll,
		now: time.Now, sleep: sleepOrWake,
	}
}

func sleepOrWake(ctx context.Context, d time.Duration, wake <-chan struct{}) bool {
	t := time.NewTimer(d)
	defer t.Stop()

	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	case <-wake:
		return true
	}
}

// run calls try until it succeeds (true), the attempts are spent or ctx ends (false). A failure that
// says the host has no network does not spend an attempt or grow the backoff: the next try comes after
// netPoll, or sooner when wake fires (a platform that hears the network return, e.g. Android's
// ConnectivityManager). Waiting for the network stops after netWait in all; then failures count again.
func (p redialPolicy) run(ctx context.Context, wake <-chan struct{}, try func(attempt uint, waitingForNetwork bool) error) bool {
	var (
		downSince time.Time
		backoff   = p.backoff
		delay     = p.backoff
		down      bool
	)

	for attempt := uint(1); attempt <= p.attempts; {
		var w <-chan struct{}
		if down {
			w = wake
		}

		if !p.sleep(ctx, delay, w) {
			return false
		}

		err := try(attempt, down)
		if err == nil {
			return true
		}

		if networkDown(err) {
			if downSince.IsZero() {
				downSince = p.now()
			}

			if p.now().Sub(downSince) < p.netWait {
				down, delay = true, p.netPoll

				continue
			}
		}

		if !networkDown(err) {
			downSince = time.Time{} // the network was back: a later outage gets its own wait
		}

		down = false
		attempt++
		backoff *= 2
		delay = backoff
	}

	return false
}
