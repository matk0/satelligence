package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

func NewRouter(nwcHandler *NWCHandler, responsesHandler *ResponsesHandler, walletHandler *WalletHandler, modelFeed ModelLister) http.Handler {
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
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-NWC")
			w.Header().Set("Access-Control-Expose-Headers", "X-Charged-Sats, X-Cost-Sats, X-Cost-USD, X-Refund-Sats, X-Refund-Status")

			if r.Method == "OPTIONS" {
				w.WriteHeader(http.StatusOK)
				return
			}

			next.ServeHTTP(w, r)
		})
	})

	// Health check
	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok"}`))
	})

	// List available models (for agent discovery)
	r.Get("/v1/models", func(w http.ResponseWriter, r *http.Request) {
		models := modelFeed.GetModels()
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"models":[`))
		for i, m := range models {
			if i > 0 {
				w.Write([]byte(","))
			}
			w.Write([]byte(`"` + m + `"`))
		}
		w.Write([]byte(`]}`))
	})

	// NWC chat completions - the primary API for AI agents
	r.Post("/v1/chat/completions", nwcHandler.ChatCompletions)
	r.Post("/v1/chat/completions/stream", nwcHandler.ChatCompletionsStream)

	// OpenAI Responses API (/v1/responses) - newer API format
	r.Post("/v1/responses", responsesHandler.Responses)

	// Hosted wallet management (optional - requires LNbits)
	if walletHandler != nil {
		r.Post("/v1/wallet/create", walletHandler.CreateWallet)
		r.Get("/v1/wallet/{wallet_id}", walletHandler.GetWallet)
		r.Post("/v1/wallet/{wallet_id}/deposit", walletHandler.CreateDeposit)
	}

	return r
}

// ModelLister interface for dependency injection
type ModelLister interface {
	GetModels() []string
}
