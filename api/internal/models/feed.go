package models

import (
	"context"

	"github.com/trandor/trandor/internal/billing"
)

// ModelFeed provides the list of available models
type ModelFeed struct {
	models []string
}

// NewModelFeed creates a new ModelFeed
func NewModelFeed(_ string, _ map[string]billing.ModelPrice) *ModelFeed {
	return &ModelFeed{
		models: []string{"gpt-5.2"},
	}
}

// Start is a no-op (kept for interface compatibility)
func (f *ModelFeed) Start(_ context.Context) {}

// GetModels returns the list of available models
func (f *ModelFeed) GetModels() []string {
	return f.models
}

// IsSupported checks if a model is supported
func (f *ModelFeed) IsSupported(model string) bool {
	return model == "gpt-5.2"
}
