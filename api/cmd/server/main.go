package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/joho/godotenv"
	"github.com/trandor/trandor/config"
	"github.com/trandor/trandor/internal/api"
	"github.com/trandor/trandor/internal/billing"
	"github.com/trandor/trandor/internal/blink"
	"github.com/trandor/trandor/internal/models"
	"github.com/trandor/trandor/internal/lnbits"
	"github.com/trandor/trandor/internal/provider"
	"github.com/trandor/trandor/internal/provider/openai"
	"github.com/trandor/trandor/internal/treasury"
)

func main() {
	// Load .env file if present
	godotenv.Load()

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

	// Initialize Blink client (Lightning payments)
	blinkClient := blink.NewClient(cfg.BlinkAPIKey)

	// Initialize treasury manager (BTC-to-Stablesats conversion)
	treasuryMgr := treasury.NewManager(blinkClient, cfg.Treasury)
	go treasuryMgr.Start(context.Background())

	// Initialize price feed (BTC/USD)
	priceFeed := blink.NewPriceFeed(blinkClient)
	go priceFeed.Start(context.Background())

	// Initialize billing calculator
	billingCalc := billing.NewCalculator(priceFeed, cfg.MarkupPercent)

	// Initialize AI provider
	openaiProvider := openai.NewProvider(cfg.OpenAIAPIKey)
	providerRouter := provider.NewRouter()
	for model := range billing.ModelPricing {
		providerRouter.Register(model, openaiProvider)
	}

	// Initialize model feed
	modelFeed := models.NewModelFeed(cfg.OpenAIAPIKey, billing.ModelPricing)
	go modelFeed.Start(context.Background())

	// Initialize NWC handler (the only payment method)
	nwcHandler := api.NewNWCHandler(
		providerRouter,
		billingCalc,
		blinkClient,
		openaiProvider, // for content moderation
		modelFeed,
		cfg,
	)

	// Initialize Responses API handler
	responsesHandler := api.NewResponsesHandler(
		openaiProvider,
		billingCalc,
		blinkClient,
		modelFeed,
		cfg,
	)

	// Initialize LNbits client for hosted wallets (optional)
	var walletHandler *api.WalletHandler
	if cfg.LNbitsEnabled() {
		lnbitsClient := lnbits.NewClient(cfg.LNbitsURL, cfg.LNbitsAdminKey)
		walletHandler = api.NewWalletHandler(lnbitsClient)
		slog.Info("hosted wallets enabled", "lnbits_url", cfg.LNbitsURL)
	} else {
		slog.Info("hosted wallets disabled (LNBITS_ADMIN_KEY not set)")
	}

	// Setup router
	router := api.NewRouter(nwcHandler, responsesHandler, walletHandler, modelFeed, treasuryMgr)

	// Create server
	server := &http.Server{
		Addr:         ":" + cfg.Port,
		Handler:      router,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 120 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	// Start server
	go func() {
		slog.Info("starting Trandor API server", "port", cfg.Port)
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

	// Stop treasury manager
	treasuryMgr.Stop()

	// Graceful shutdown
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		slog.Error("server forced to shutdown", "error", err)
	}

	slog.Info("server stopped")
}
