package config

import (
	"os"
	"strconv"
)

type Config struct {
	// External APIs
	BlinkAPIKey  string
	OpenAIAPIKey string

	// Billing
	MarkupPercent float64

	// Abuse protection
	MaxStrikes int

	// Server
	Port string
}

func Load() *Config {
	return &Config{
		BlinkAPIKey:   getEnv("BLINK_API_KEY", ""),
		OpenAIAPIKey:  getEnv("OPENAI_API_KEY", ""),
		MarkupPercent: getEnvFloat("MARKUP_PERCENT", 5.0),
		MaxStrikes:    getEnvInt("MAX_STRIKES", 3),
		Port:          getEnv("API_PORT", "8080"),
	}
}

func (c *Config) Validate() error {
	if c.BlinkAPIKey == "" {
		return ErrMissingBlinkAPIKey
	}
	if c.OpenAIAPIKey == "" {
		return ErrMissingOpenAIAPIKey
	}
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

func getEnvFloat(key string, defaultValue float64) float64 {
	if value := os.Getenv(key); value != "" {
		if f, err := strconv.ParseFloat(value, 64); err == nil {
			return f
		}
	}
	return defaultValue
}
