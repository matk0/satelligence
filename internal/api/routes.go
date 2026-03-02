package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/satilligence/satilligence/config"
	"github.com/satilligence/satilligence/internal/l402"
)

func NewRouter(handler *Handler, l402Service *l402.Service, cfg *config.Config) http.Handler {
	r := chi.NewRouter()

	// Global middleware
	r.Use(RecoveryMiddleware)
	r.Use(LoggingMiddleware)
	r.Use(middleware.RealIP)
	r.Use(middleware.RequestID)

	// Health check (no auth required)
	r.Get("/health", handler.Health)

	// List models (no auth required)
	r.Get("/v1/models", handler.ListModels)

	// Protected routes
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
