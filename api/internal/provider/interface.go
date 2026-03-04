package provider

import (
	"context"
	"encoding/json"
)

// MessageContent can be either a string or an array of content parts
// This supports both formats per OpenAI spec:
// - String: "Hello"
// - Array: [{"type": "text", "text": "Hello"}, {"type": "image_url", ...}]
type MessageContent struct {
	raw json.RawMessage
}

// UnmarshalJSON handles both string and array content formats
func (c *MessageContent) UnmarshalJSON(data []byte) error {
	c.raw = data
	return nil
}

// MarshalJSON returns the raw content as-is
func (c MessageContent) MarshalJSON() ([]byte, error) {
	if c.raw == nil {
		return []byte(`""`), nil
	}
	return c.raw, nil
}

// String returns the text content, extracting from array if needed
// This is used for moderation and cost estimation
func (c *MessageContent) String() string {
	if c.raw == nil || len(c.raw) == 0 {
		return ""
	}

	// Try as string first
	var str string
	if err := json.Unmarshal(c.raw, &str); err == nil {
		return str
	}

	// Try as array of content parts
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(c.raw, &parts); err == nil {
		var result string
		for _, part := range parts {
			if part.Type == "text" && part.Text != "" {
				if result != "" {
					result += "\n"
				}
				result += part.Text
			}
		}
		return result
	}

	return ""
}

// ChatMessage represents a message in the chat
type ChatMessage struct {
	Role    string         `json:"role"`
	Content MessageContent `json:"content"`
}

// ChatStreamOptions contains options for streaming
type ChatStreamOptions struct {
	IncludeUsage bool `json:"include_usage,omitempty"`
}

// ChatRequest represents a chat completion request
type ChatRequest struct {
	Model         string             `json:"model"`
	Messages      []ChatMessage      `json:"messages"`
	MaxTokens     int                `json:"max_tokens,omitempty"`
	Temperature   *float64           `json:"temperature,omitempty"`
	TopP          *float64           `json:"top_p,omitempty"`
	N             int                `json:"n,omitempty"`
	Stop          []string           `json:"stop,omitempty"`
	Stream        bool               `json:"stream,omitempty"`
	StreamOptions *ChatStreamOptions `json:"stream_options,omitempty"`
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

// ResponsesRequest represents a request to the Responses API (/v1/responses)
type ResponsesRequest struct {
	Model           string                 `json:"model"`
	Input           interface{}            `json:"input"` // Can be string or array of messages
	Instructions    string                 `json:"instructions,omitempty"`
	MaxOutputTokens int                    `json:"max_output_tokens,omitempty"`
	Temperature     *float64               `json:"temperature,omitempty"`
	TopP            *float64               `json:"top_p,omitempty"`
	Tools           []map[string]interface{} `json:"tools,omitempty"`
	Stream          bool                   `json:"stream,omitempty"`
	Store           *bool                  `json:"store,omitempty"`
}

// ResponsesOutputContent represents content in a response output message
type ResponsesOutputContent struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// ResponsesOutputItem represents an item in the output array
type ResponsesOutputItem struct {
	Type    string                   `json:"type"`
	ID      string                   `json:"id,omitempty"`
	Status  string                   `json:"status,omitempty"`
	Role    string                   `json:"role,omitempty"`
	Content []ResponsesOutputContent `json:"content,omitempty"`
}

// ResponsesUsage represents token usage in the Responses API
type ResponsesUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

// ResponsesResponse represents a response from the Responses API
type ResponsesResponse struct {
	ID        string                `json:"id"`
	Object    string                `json:"object"` // "response"
	CreatedAt int64                 `json:"created_at"`
	Status    string                `json:"status"`
	Model     string                `json:"model"`
	Output    []ResponsesOutputItem `json:"output"`
	Usage     ResponsesUsage        `json:"usage"`
}

// ResponsesProvider extends Provider with Responses API support
type ResponsesProvider interface {
	Provider
	Responses(ctx context.Context, req *ResponsesRequest) (*ResponsesResponse, error)
	ResponsesStream(ctx context.Context, req *ResponsesRequest) (*StreamReader, error)
}
