package provider

import (
	"errors"
	"sync"
)

var (
	ErrModelNotFound     = errors.New("model not found")
	ErrProviderNotFound  = errors.New("provider not found for model")
	ErrModelNotSupported = errors.New("model not supported")
)

// Router routes requests to the appropriate provider based on model
type Router struct {
	mu        sync.RWMutex
	providers map[string]Provider
}

func NewRouter() *Router {
	return &Router{
		providers: make(map[string]Provider),
	}
}

// Register associates a model name with a provider
func (r *Router) Register(model string, provider Provider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.providers[model] = provider
}

// GetProvider returns the provider for a given model
func (r *Router) GetProvider(model string) (Provider, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	provider, ok := r.providers[model]
	if !ok {
		return nil, ErrModelNotFound
	}

	return provider, nil
}
