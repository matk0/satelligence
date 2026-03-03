package openai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/trandor/trandor/internal/provider"
)

const (
	OpenAIBaseURL = "https://api.openai.com/v1"
)

var supportedModels = map[string]bool{
	"gpt-5.2": true,
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

// GPT5Request is the request format for GPT-5.x models which use max_completion_tokens
type GPT5Request struct {
	Model               string                   `json:"model"`
	Messages            []provider.ChatMessage   `json:"messages"`
	MaxCompletionTokens int                      `json:"max_completion_tokens,omitempty"`
	Temperature         *float64                 `json:"temperature,omitempty"`
	TopP                *float64                 `json:"top_p,omitempty"`
	N                   int                      `json:"n,omitempty"`
	Stop                []string                 `json:"stop,omitempty"`
}

func (p *Provider) Chat(ctx context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
	if !p.SupportsModel(req.Model) {
		return nil, provider.ErrModelNotSupported
	}

	// GPT-5.2 uses max_completion_tokens instead of max_tokens
	gpt5Req := GPT5Request{
		Model:               req.Model,
		Messages:            req.Messages,
		MaxCompletionTokens: req.MaxTokens,
		Temperature:         req.Temperature,
		TopP:                req.TopP,
		N:                   req.N,
		Stop:                req.Stop,
	}

	body, err := json.Marshal(gpt5Req)
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

	// Log raw response for debugging
	slog.Info("openai raw response", "status", resp.StatusCode, "body_length", len(respBody))
	slog.Debug("openai response body", "body", string(respBody))

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
		return nil, fmt.Errorf("openai error: status %d, body: %s", resp.StatusCode, string(respBody))
	}

	// Parse the response flexibly to handle both string and array content formats
	var rawResp map[string]interface{}
	if err := json.Unmarshal(respBody, &rawResp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	// Try to extract content from the response
	var chatResp provider.ChatResponse
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		slog.Error("failed to parse response", "error", err, "body", string(respBody))
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	// Check if content is an array (new GPT-5.x format) and convert it
	if len(chatResp.Choices) > 0 && chatResp.Choices[0].Message.Content == "" {
		// Try to parse content as array of content parts
		if choices, ok := rawResp["choices"].([]interface{}); ok && len(choices) > 0 {
			if choice, ok := choices[0].(map[string]interface{}); ok {
				if message, ok := choice["message"].(map[string]interface{}); ok {
					if contentArray, ok := message["content"].([]interface{}); ok {
						// Content is an array, extract text from parts
						var textParts []string
						for _, part := range contentArray {
							if partMap, ok := part.(map[string]interface{}); ok {
								if partType, ok := partMap["type"].(string); ok && partType == "text" {
									if text, ok := partMap["text"].(string); ok {
										textParts = append(textParts, text)
									}
								}
							}
						}
						if len(textParts) > 0 {
							chatResp.Choices[0].Message.Content = strings.Join(textParts, "\n")
							slog.Info("extracted content from array format", "content_length", len(chatResp.Choices[0].Message.Content))
						}
					}
				}
			}
		}
	}

	slog.Info("parsed response", "choices", len(chatResp.Choices), "content_length", len(chatResp.Choices[0].Message.Content))

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

// GPT5StreamRequest is the streaming request format for GPT-5.x
type GPT5StreamRequest struct {
	Model               string                   `json:"model"`
	Messages            []provider.ChatMessage   `json:"messages"`
	MaxCompletionTokens int                      `json:"max_completion_tokens,omitempty"`
	Temperature         *float64                 `json:"temperature,omitempty"`
	TopP                *float64                 `json:"top_p,omitempty"`
	N                   int                      `json:"n,omitempty"`
	Stop                []string                 `json:"stop,omitempty"`
	Stream              bool                     `json:"stream"`
	StreamOptions       *struct {
		IncludeUsage bool `json:"include_usage"`
	} `json:"stream_options,omitempty"`
}

// ChatStream creates a streaming chat completion
func (p *Provider) ChatStream(ctx context.Context, req *provider.ChatRequest) (*provider.StreamReader, error) {
	if !p.SupportsModel(req.Model) {
		return nil, provider.ErrModelNotSupported
	}

	// GPT-5.2 uses max_completion_tokens
	streamReq := GPT5StreamRequest{
		Model:               req.Model,
		Messages:            req.Messages,
		MaxCompletionTokens: req.MaxTokens,
		Temperature:         req.Temperature,
		TopP:                req.TopP,
		N:                   req.N,
		Stop:                req.Stop,
		Stream:              true,
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

// Responses calls the OpenAI Responses API (/v1/responses)
func (p *Provider) Responses(ctx context.Context, req *provider.ResponsesRequest) (*provider.ResponsesResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", OpenAIBaseURL+"/responses", bytes.NewReader(body))
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

	slog.Info("openai responses raw response", "status", resp.StatusCode, "body_length", len(respBody))
	slog.Debug("openai responses body", "body", string(respBody))

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
		return nil, fmt.Errorf("openai error: status %d, body: %s", resp.StatusCode, string(respBody))
	}

	var responsesResp provider.ResponsesResponse
	if err := json.Unmarshal(respBody, &responsesResp); err != nil {
		slog.Error("failed to parse responses response", "error", err, "body", string(respBody))
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	return &responsesResp, nil
}

// ResponsesStream creates a streaming Responses API call
func (p *Provider) ResponsesStream(ctx context.Context, req *provider.ResponsesRequest) (*provider.StreamReader, error) {
	// Ensure stream is set
	req.Stream = true

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", OpenAIBaseURL+"/responses", bytes.NewReader(body))
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
