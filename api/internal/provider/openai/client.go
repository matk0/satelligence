package openai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/trandor/trandor/internal/provider"
)

const (
	OpenAIBaseURL = "https://api.openai.com/v1"
)

var supportedModels = map[string]bool{
	"gpt-4o":        true,
	"gpt-4o-mini":   true,
	"gpt-4-turbo":   true,
	"gpt-4":         true,
	"gpt-3.5-turbo": true,
}

type Provider struct {
	apiKey     string
	httpClient *http.Client
}

func NewProvider(apiKey string) *Provider {
	return &Provider{
		apiKey: apiKey,
		httpClient: &http.Client{
			Timeout: 120 * time.Second,
		},
	}
}

func (p *Provider) Chat(ctx context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
	if !p.SupportsModel(req.Model) {
		return nil, provider.ErrModelNotSupported
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", OpenAIBaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		var errResp struct {
			Error struct {
				Message string `json:"message"`
				Type    string `json:"type"`
				Code    string `json:"code"`
			} `json:"error"`
		}
		if err := json.Unmarshal(respBody, &errResp); err == nil && errResp.Error.Message != "" {
			return nil, &OpenAIError{
				StatusCode: resp.StatusCode,
				Message:    errResp.Error.Message,
				Type:       errResp.Error.Type,
				Code:       errResp.Error.Code,
			}
		}
		return nil, fmt.Errorf("openai error: status %d", resp.StatusCode)
	}

	var chatResp provider.ChatResponse
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	return &chatResp, nil
}

func (p *Provider) SupportsModel(model string) bool {
	return supportedModels[model]
}

type OpenAIError struct {
	StatusCode int
	Message    string
	Type       string
	Code       string
}

func (e *OpenAIError) Error() string {
	return fmt.Sprintf("openai error: %s (type: %s, code: %s)", e.Message, e.Type, e.Code)
}

// ChatStream creates a streaming chat completion
func (p *Provider) ChatStream(ctx context.Context, req *provider.ChatRequest) (*provider.StreamReader, error) {
	if !p.SupportsModel(req.Model) {
		return nil, provider.ErrModelNotSupported
	}

	// Create streaming request
	streamReq := struct {
		*provider.ChatRequest
		Stream         bool `json:"stream"`
		StreamOptions  *struct {
			IncludeUsage bool `json:"include_usage"`
		} `json:"stream_options,omitempty"`
	}{
		ChatRequest: req,
		Stream:      true,
		StreamOptions: &struct {
			IncludeUsage bool `json:"include_usage"`
		}{IncludeUsage: true},
	}

	body, err := json.Marshal(streamReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", OpenAIBaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)

	// Use a client without timeout for streaming
	client := &http.Client{}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		respBody, _ := io.ReadAll(resp.Body)
		var errResp struct {
			Error struct {
				Message string `json:"message"`
				Type    string `json:"type"`
				Code    string `json:"code"`
			} `json:"error"`
		}
		if err := json.Unmarshal(respBody, &errResp); err == nil && errResp.Error.Message != "" {
			return nil, &OpenAIError{
				StatusCode: resp.StatusCode,
				Message:    errResp.Error.Message,
				Type:       errResp.Error.Type,
				Code:       errResp.Error.Code,
			}
		}
		return nil, fmt.Errorf("openai error: status %d", resp.StatusCode)
	}

	scanner := bufio.NewScanner(resp.Body)
	return provider.NewStreamReader(resp.Body, resp.Body.Close, scanner), nil
}

// ParseStreamChunk parses a SSE data line into a StreamChunk
func ParseStreamChunk(line string) (*provider.StreamChunk, error) {
	if !strings.HasPrefix(line, "data: ") {
		return nil, nil
	}
	data := strings.TrimPrefix(line, "data: ")
	if data == "[DONE]" {
		return nil, nil
	}
	var chunk provider.StreamChunk
	if err := json.Unmarshal([]byte(data), &chunk); err != nil {
		return nil, err
	}
	return &chunk, nil
}

// Moderate checks content against OpenAI's moderation API
func (p *Provider) Moderate(ctx context.Context, input string) (*provider.ModerationResult, error) {
	reqBody := map[string]string{"input": input}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", OpenAIBaseURL+"/moderations", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("moderation error: status %d", resp.StatusCode)
	}

	var modResp struct {
		Results []struct {
			Flagged    bool            `json:"flagged"`
			Categories map[string]bool `json:"categories"`
		} `json:"results"`
	}

	if err := json.Unmarshal(respBody, &modResp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	if len(modResp.Results) == 0 {
		return &provider.ModerationResult{Flagged: false}, nil
	}

	result := &provider.ModerationResult{
		Flagged:    modResp.Results[0].Flagged,
		Categories: modResp.Results[0].Categories,
	}

	// Find first flagged category for reason
	if result.Flagged {
		for cat, flagged := range result.Categories {
			if flagged {
				result.Reason = cat
				break
			}
		}
	}

	return result, nil
}
