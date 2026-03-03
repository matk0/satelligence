package config

import (
	"os"
	"strconv"
	"time"
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

	// Treasury management
	Treasury TreasuryConfig
}

// TreasuryConfig controls automatic BTC-to-Stablesats conversion
type TreasuryConfig struct {
	Enabled       bool          // Enable treasury management
	ThresholdSats int64         // Convert when BTC exceeds this amount
	MinSats       int64         // Minimum amount to convert
	Interval      time.Duration // How often to check balances
	RetainBuffer  int64         // Keep this amount in BTC for refunds
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
		Treasury: TreasuryConfig{
			Enabled:       getEnvBool("TREASURY_ENABLED", false),
			ThresholdSats: getEnvInt64("TREASURY_THRESHOLD_SATS", 1000),
			MinSats:       getEnvInt64("TREASURY_MIN_SATS", 500),
			Interval:      getEnvDuration("TREASURY_INTERVAL", 5*time.Minute),
			RetainBuffer:  getEnvInt64("TREASURY_RETAIN_BUFFER", 2000),
		},
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

func getEnvInt64(key string, defaultValue int64) int64 {
	if value := os.Getenv(key); value != "" {
		if i, err := strconv.ParseInt(value, 10, 64); err == nil {
			return i
		}
	}
	return defaultValue
}

func getEnvBool(key string, defaultValue bool) bool {
	if value := os.Getenv(key); value != "" {
		if b, err := strconv.ParseBool(value); err == nil {
			return b
		}
	}
	return defaultValue
}

func getEnvDuration(key string, defaultValue time.Duration) time.Duration {
	if value := os.Getenv(key); value != "" {
		if d, err := time.ParseDuration(value); err == nil {
			return d
		}
	}
	return defaultValue
}
