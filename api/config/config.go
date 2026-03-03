package config

import (
	"os"
	"strconv"
)

type Config struct {
	// External APIs
	BlinkAPIKey  string
	OpenAIAPIKey string

	// LNbits (hosted wallets)
	LNbitsURL      string
	LNbitsAdminKey string

	// Billing
	MarkupPercent float64

	// Abuse protection
	MaxStrikes int

	// Server
	Port string
}

func Load() *Config {
	return &Config{
		BlinkAPIKey:    getEnv("BLINK_API_KEY", ""),
		OpenAIAPIKey:   getEnv("OPENAI_API_KEY", ""),
		LNbitsURL:      getEnv("LNBITS_URL", "http://lnbits:5000"),
		LNbitsAdminKey: getEnv("LNBITS_ADMIN_KEY", ""),
		MarkupPercent:  getEnvFloat("MARKUP_PERCENT", 5.0),
		MaxStrikes:     getEnvInt("MAX_STRIKES", 3),
		Port:           getEnv("API_PORT", "8080"),
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

// LNbitsEnabled returns true if LNbits hosted wallets are configured
func (c *Config) LNbitsEnabled() bool {
	return c.LNbitsURL != "" && c.LNbitsAdminKey != ""
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
