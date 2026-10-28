package auth

import (
	"sync"
	"time"
)


// How many Allow calls between sweeps of idle requesters
const sweep_every = 500


type Limiter struct {
	mu      sync.Mutex
	reqs    map[string][]time.Time
	max_req int
	window  time.Duration
	count   int // Allow calls since the last sweep
}


func newLimiter(max_req int, window time.Duration) *Limiter {
	return &Limiter{
		reqs: map[string][]time.Time{},
		max_req: max_req,
		window: window,
	}
}


func (l *Limiter) Allow(requester string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-l.window)

	// Periodically drop requesters with no live requests, capping map growth
	l.count++
	if l.count >= sweep_every {
		l.sweep(cutoff)
		l.count = 0
	}

	// Drop expired requests (times are appended in order, so find the first live one)
	reqs := l.reqs[requester]
	i := 0
	for i < len(reqs) && !reqs[i].After(cutoff) { i++ }
	reqs = reqs[i:]

	// Check that it is valid
	if len(reqs) >= l.max_req {
		l.reqs[requester] = reqs
		return false
	}

	// Add the new request
	l.reqs[requester] = append(reqs, now)
	return true
}


// Sweep removes requesters with no live requests
func (l *Limiter) Sweep() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sweep(time.Now().Add(-l.window))
}


// sweep assumes l.mu is held
func (l *Limiter) sweep(cutoff time.Time) {
	for k, reqs := range l.reqs {
		if len(reqs) == 0 || !reqs[len(reqs)-1].After(cutoff) { delete(l.reqs, k) }
	}
}
