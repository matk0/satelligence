package models

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/trandor/trandor/internal/billing"
)

// ModelFeed fetches and caches available models from OpenAI
type ModelFeed struct {
	mu      sync.RWMutex
	models  []string
	client  *http.Client
	apiKey  string
	pricing map[string]billing.ModelPrice
}

// OpenAI models list response
type openAIModelsResponse struct {
	Data []struct {
		ID      string `json:"id"`
		OwnedBy string `json:"owned_by"`
	} `json:"data"`
}

// NewModelFeed creates a new ModelFeed
func NewModelFeed(apiKey string, pricing map[string]billing.ModelPrice) *ModelFeed {
	return &ModelFeed{
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
		apiKey:  apiKey,
		pricing: pricing,
	}
}

// Start begins the background refresh loop
func (f *ModelFeed) Start(ctx context.Context) {
	// Initial fetch
	if err := f.fetchModels(ctx); err != nil {
		slog.Error("failed to fetch initial models", "error", err)
		// Fall back to models from pricing table
		f.mu.Lock()
		f.models = f.getPricedModels()
		f.mu.Unlock()
		slog.Info("using fallback models from pricing table", "count", len(f.models))
	}

	// Refresh every 6 hours
	ticker := time.NewTicker(6 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := f.fetchModels(ctx); err != nil {
				slog.Error("failed to fetch models", "error", err)
				// Keep serving cached models on failure
			}
		}
	}
}

func (f *ModelFeed) fetchModels(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, "GET", "https://api.openai.com/v1/models", nil)
	if err != nil {
		return err
	}

	req.Header.Set("Authorization", "Bearer "+f.apiKey)

	resp, err := f.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		slog.Error("OpenAI models API returned non-200", "status", resp.StatusCode)
		return nil // Don't return error, just keep cached models
	}

	var result openAIModelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return err
	}

	// Filter and sort models
	var models []string
	for _, model := range result.Data {
		// Only include gpt-* models
		if !strings.HasPrefix(model.ID, "gpt-") {
			continue
		}

		// Only include models owned by openai or system
		if model.OwnedBy != "openai" && model.OwnedBy != "system" && model.OwnedBy != "openai-internal" {
			continue
		}

		// Only include models we have pricing for
		if _, ok := f.pricing[model.ID]; !ok {
			continue
		}

		models = append(models, model.ID)
	}

	// Sort models for consistent ordering
	sort.Strings(models)

	f.mu.Lock()
	f.models = models
	f.mu.Unlock()

	slog.Info("models refreshed", "count", len(models))
	return nil
}

// getPricedModels returns all models that have pricing configured
func (f *ModelFeed) getPricedModels() []string {
	var models []string
	for model := range f.pricing {
		models = append(models, model)
	}
	sort.Strings(models)
	return models
}

// GetModels returns the list of available models
func (f *ModelFeed) GetModels() []string {
	f.mu.RLock()
	defer f.mu.RUnlock()

	// Return a copy to prevent modification
	result := make([]string, len(f.models))
	copy(result, f.models)
	return result
}

// IsSupported checks if a model is supported
func (f *ModelFeed) IsSupported(model string) bool {
	// First check if we have pricing for it
	if _, ok := f.pricing[model]; !ok {
		return false
	}

	// Then check if it's in our fetched list (if we have one)
	f.mu.RLock()
	defer f.mu.RUnlock()

	// If we haven't fetched models yet, allow any priced model
	if len(f.models) == 0 {
		return true
	}

	for _, m := range f.models {
		if m == model {
			return true
		}
	}
	return false
}
