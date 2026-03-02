package config

import (
	"os"
	"strconv"
)

type Config struct {
	// Database
	DatabaseURL string

	// External APIs
	BlinkAPIKey  string
	OpenAIAPIKey string

	// Security
	MacaroonSecret string

	// Billing
	MarkupPercent  float64
	MinDepositSats int64

	// Rate limiting
	RateLimitRPM int

	// Abuse protection
	MaxStrikes int

	// Server
	Port string
}

func Load() *Config {
	return &Config{
		DatabaseURL:    getEnv("DATABASE_URL", ""),
		BlinkAPIKey:    getEnv("BLINK_API_KEY", ""),
		OpenAIAPIKey:   getEnv("OPENAI_API_KEY", ""),
		MacaroonSecret: getEnv("MACAROON_SECRET", ""),
		MarkupPercent:  getEnvFloat("MARKUP_PERCENT", 5.0),
		MinDepositSats: getEnvInt64("MIN_DEPOSIT_SATS", 5000),
		RateLimitRPM:   getEnvInt("RATE_LIMIT_RPM", 60),
		MaxStrikes:     getEnvInt("MAX_STRIKES", 3),
		Port:           getEnv("PORT", "8080"),
	}
}

func (c *Config) Validate() error {
	if c.DatabaseURL == "" {
		return ErrMissingDatabaseURL
	}
	if c.MacaroonSecret == "" {
		return ErrMissingMacaroonSecret
	}
	// Blink and OpenAI keys are optional for local development
	// but required for full functionality
	return nil
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if i, err := strconv.Atoi(value); err == nil {
			return i
		}
	}
	return defaultValue
}

func getEnvInt64(key string, defaultValue int64) int64 {
	if value := os.Getenv(key); value != "" {
		if i, err := strconv.ParseInt(value, 10, 64); err == nil {
			return i
		}
	}
	return defaultValue
}

func getEnvFloat(key string, defaultValue float64) float64 {
	if value := os.Getenv(key); value != "" {
		if f, err := strconv.ParseFloat(value, 64); err == nil {
			return f
		}
	}
	return defaultValue
}
