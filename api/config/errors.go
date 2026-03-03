package config

import "errors"

var (
	ErrMissingBlinkAPIKey  = errors.New("BLINK_API_KEY is required")
	ErrMissingOpenAIAPIKey = errors.New("OPENAI_API_KEY is required")
)
