package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/satilligence/satilligence/config"
	"github.com/satilligence/satilligence/internal/api"
	"github.com/satilligence/satilligence/internal/billing"
	"github.com/satilligence/satilligence/internal/blink"
	"github.com/satilligence/satilligence/internal/db"
	"github.com/satilligence/satilligence/internal/l402"
	"github.com/satilligence/satilligence/internal/provider"
	"github.com/satilligence/satilligence/internal/provider/openai"
	"github.com/satilligence/satilligence/internal/session"
)

func main() {
	// Setup structured logging
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	// Load configuration
	cfg := config.Load()
	if err := cfg.Validate(); err != nil {
		slog.Error("invalid configuration", "error", err)
		os.Exit(1)
	}

	// Connect to database
	database, err := db.Connect(context.Background(), cfg.DatabaseURL)
	if err != nil {
		slog.Error("failed to connect to database", "error", err)
		os.Exit(1)
	}
	defer database.Close()

	// Run migrations
	if err := database.Migrate(context.Background()); err != nil {
		slog.Error("failed to run migrations", "error", err)
		os.Exit(1)
	}

	// Initialize Blink client
	blinkClient := blink.NewClient(cfg.BlinkAPIKey)

	// Initialize price feed
	priceFeed := blink.NewPriceFeed(blinkClient)
	go priceFeed.Start(context.Background())

	// Initialize session store
	sessionStore := session.NewStore(database)

	// Initialize L402 service
	l402Service := l402.NewService(cfg.MacaroonSecret, blinkClient, sessionStore, cfg.MinDepositSats)

	// Initialize billing calculator
	billingCalc := billing.NewCalculator(priceFeed, cfg.MarkupPercent)

	// Initialize provider router
	openaiProvider := openai.NewProvider(cfg.OpenAIAPIKey)
	providerRouter := provider.NewRouter()
	providerRouter.Register("gpt-4o", openaiProvider)
	providerRouter.Register("gpt-4o-mini", openaiProvider)
	providerRouter.Register("gpt-4-turbo", openaiProvider)

	// Initialize API handler
	handler := api.NewHandler(
		l402Service,
		sessionStore,
		providerRouter,
		billingCalc,
		openaiProvider, // for moderation
		cfg,
	)

	// Setup router
	router := api.NewRouter(handler, l402Service, cfg)

	// Create server
	server := &http.Server{
		Addr:         ":" + cfg.Port,
		Handler:      router,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 120 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	// Start server in goroutine
	go func() {
		slog.Info("starting server", "port", cfg.Port)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	// Wait for interrupt signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	slog.Info("shutting down server...")

	// Graceful shutdown with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		slog.Error("server forced to shutdown", "error", err)
	}

	slog.Info("server stopped")
}
