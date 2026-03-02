package provider

import (
	"context"
)

// ChatMessage represents a message in the chat
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ChatRequest represents a chat completion request
type ChatRequest struct {
	Model       string        `json:"model"`
	Messages    []ChatMessage `json:"messages"`
	MaxTokens   int           `json:"max_tokens,omitempty"`
	Temperature *float64      `json:"temperature,omitempty"`
	TopP        *float64      `json:"top_p,omitempty"`
	N           int           `json:"n,omitempty"`
	Stop        []string      `json:"stop,omitempty"`
}

// ChatChoice represents a single completion choice
type ChatChoice struct {
	Index        int         `json:"index"`
	Message      ChatMessage `json:"message"`
	FinishReason string      `json:"finish_reason"`
}

// ChatUsage represents token usage
type ChatUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// ChatResponse represents a chat completion response
type ChatResponse struct {
	ID      string       `json:"id"`
	Object  string       `json:"object"`
	Created int64        `json:"created"`
	Model   string       `json:"model"`
	Choices []ChatChoice `json:"choices"`
	Usage   ChatUsage    `json:"usage"`
}

// Provider defines the interface for AI model providers
type Provider interface {
	Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error)
	SupportsModel(model string) bool
}

// StreamProvider extends Provider with streaming support
type StreamProvider interface {
	Provider
	ChatStream(ctx context.Context, req *ChatRequest) (*StreamReader, error)
}

// StreamChunk represents a single chunk from a streaming response
type StreamChunk struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	Model   string `json:"model"`
	Choices []struct {
		Index int `json:"index"`
		Delta struct {
			Role    string `json:"role,omitempty"`
			Content string `json:"content,omitempty"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage *ChatUsage `json:"usage,omitempty"`
}

// StreamReader wraps the streaming response
type StreamReader struct {
	reader   interface{ Read([]byte) (int, error) }
	closer   func() error
	scanner  interface{ Scan() bool; Text() string; Err() error }
}

func NewStreamReader(reader interface{ Read([]byte) (int, error) }, closer func() error, scanner interface{ Scan() bool; Text() string; Err() error }) *StreamReader {
	return &StreamReader{reader: reader, closer: closer, scanner: scanner}
}

func (s *StreamReader) Next() (string, bool, error) {
	if !s.scanner.Scan() {
		if err := s.scanner.Err(); err != nil {
			return "", false, err
		}
		return "", false, nil
	}
	return s.scanner.Text(), true, nil
}

func (s *StreamReader) Close() error {
	if s.closer != nil {
		return s.closer()
	}
	return nil
}

// ModerationResult represents content moderation output
type ModerationResult struct {
	Flagged    bool
	Categories map[string]bool
	Reason     string
}

// Moderator defines the interface for content moderation
type Moderator interface {
	Moderate(ctx context.Context, input string) (*ModerationResult, error)
}
