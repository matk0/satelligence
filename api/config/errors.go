package config

import "errors"

var (
	ErrMissingDatabaseURL    = errors.New("DATABASE_URL is required")
	ErrMissingBlinkAPIKey    = errors.New("BLINK_API_KEY is required")
	ErrMissingOpenAIAPIKey   = errors.New("OPENAI_API_KEY is required")
	ErrMissingMacaroonSecret = errors.New("MACAROON_SECRET is required")
)
