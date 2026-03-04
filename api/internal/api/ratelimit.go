package api

import (
	"errors"
	"sync"
)

var ErrTooManyConcurrentRequests = errors.New("too many concurrent requests")

// WalletRateLimiter limits concurrent requests per wallet pubkey
type WalletRateLimiter struct {
	mu      sync.Mutex
	active  map[string]int // walletPubkey -> active request count
	maxConc int
}

// NewWalletRateLimiter creates a new rate limiter with the given max concurrent requests per wallet
func NewWalletRateLimiter(maxConcurrent int) *WalletRateLimiter {
	return &WalletRateLimiter{
		active:  make(map[string]int),
		maxConc: maxConcurrent,
	}
}

// Acquire attempts to acquire a slot for the given wallet pubkey.
// Returns an error if the wallet has too many concurrent requests.
func (r *WalletRateLimiter) Acquire(pubkey string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	current := r.active[pubkey]
	if current >= r.maxConc {
		return ErrTooManyConcurrentRequests
	}

	r.active[pubkey] = current + 1
	return nil
}

// Release releases a slot for the given wallet pubkey.
// Should be called when a request completes (use defer after Acquire succeeds).
func (r *WalletRateLimiter) Release(pubkey string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	current := r.active[pubkey]
	if current <= 1 {
		delete(r.active, pubkey)
	} else {
		r.active[pubkey] = current - 1
	}
}

// ActiveCount returns the number of active requests for a wallet (for testing/debugging)
func (r *WalletRateLimiter) ActiveCount(pubkey string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.active[pubkey]
}
