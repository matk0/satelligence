package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/trandor/trandor/config"
	"github.com/trandor/trandor/internal/l402"
)

func NewRouter(handler *Handler, nwcHandler *NWCHandler, weblnHandler *WebLNHandler, l402Service *l402.Service, cfg *config.Config) http.Handler {
	r := chi.NewRouter()

	// Global middleware
	r.Use(RecoveryMiddleware)
	r.Use(LoggingMiddleware)
	r.Use(middleware.RealIP)
	r.Use(middleware.RequestID)

	// CORS for browser requests
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-NWC, X-Payment-Hash")
			w.Header().Set("Access-Control-Expose-Headers", "X-Charged-Sats, X-Cost-Sats, X-Refund-Sats, X-Refund-Status")

			if r.Method == "OPTIONS" {
				w.WriteHeader(http.StatusOK)
				return
			}

			next.ServeHTTP(w, r)
		})
	})

	// Health check (no auth required)
	r.Get("/health", handler.Health)

	// List models (no auth required)
	r.Get("/v1/models", handler.ListModels)

	// NWC pay-per-request endpoint (seamless)
	// Use X-NWC header with your Nostr Wallet Connect URL
	r.Post("/v1/nwc/chat/completions", nwcHandler.ChatCompletions)

	// WebLN endpoints (browser-based payments)
	r.Post("/v1/webln/quote", weblnHandler.CreateQuote)
	r.Post("/v1/webln/chat/completions", weblnHandler.ChatCompletions)
	r.Post("/v1/webln/chat/completions/stream", weblnHandler.ChatCompletionsStream)
	r.Post("/v1/webln/refund", weblnHandler.Refund)

	// L402 prepaid balance routes (legacy)
	r.Group(func(r chi.Router) {
		r.Use(L402Middleware(l402Service, handler.sessionStore))
		r.Use(RateLimitMiddleware(cfg))

		// OpenAI-compatible endpoint
		r.Post("/v1/chat/completions", handler.ChatCompletions)

		// Lightning extensions
		r.Get("/v1/balance", handler.GetBalance)
		r.Post("/v1/invoices", handler.CreateInvoice)
		r.Get("/v1/usage", handler.GetUsage)
	})

	return r
}
