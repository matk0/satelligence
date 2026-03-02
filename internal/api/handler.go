package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/satilligence/satilligence/config"
	"github.com/satilligence/satilligence/internal/billing"
	"github.com/satilligence/satilligence/internal/l402"
	"github.com/satilligence/satilligence/internal/provider"
	"github.com/satilligence/satilligence/internal/provider/openai"
	"github.com/satilligence/satilligence/internal/session"
)

type Handler struct {
	l402Service    *l402.Service
	sessionStore   *session.Store
	providerRouter *provider.Router
	billing        *billing.Calculator
	moderator      *openai.Provider
	config         *config.Config
}

func NewHandler(
	l402Service *l402.Service,
	sessionStore *session.Store,
	providerRouter *provider.Router,
	billing *billing.Calculator,
	moderator *openai.Provider,
	cfg *config.Config,
) *Handler {
	return &Handler{
		l402Service:    l402Service,
		sessionStore:   sessionStore,
		providerRouter: providerRouter,
		billing:        billing,
		moderator:      moderator,
		config:         cfg,
	}
}

func (h *Handler) Health(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (h *Handler) ChatCompletions(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Get session from context (set by middleware)
	sess := getSessionFromContext(ctx)
	if sess == nil {
		l402.WriteError(w, http.StatusInternalServerError, "internal_error", "session not found in context")
		return
	}

	// Parse request
	var req provider.ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		l402.WriteError(w, http.StatusBadRequest, "invalid_request", "failed to parse request body")
		return
	}

	// Validate model
	if !h.providerRouter.IsModelSupported(req.Model) {
		l402.WriteError(w, http.StatusBadRequest, "invalid_model", "model not supported: "+req.Model)
		return
	}

	// Set defaults
	if req.MaxTokens == 0 {
		req.MaxTokens = 4096
	}

	// Estimate max cost
	estimatedCost := h.billing.EstimateMaxCost(req.Model, req.MaxTokens)

	// Check balance
	if sess.BalanceSats < estimatedCost {
		// Need top-up
		invoice, err := h.l402Service.CreateTopUpInvoice(ctx, sess.ID.String(), estimatedCost-sess.BalanceSats+1000)
		if err != nil {
			slog.Error("failed to create top-up invoice", "error", err)
			l402.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to create invoice")
			return
		}
		l402.WriteChallenge(w, "", invoice.PaymentRequest, estimatedCost-sess.BalanceSats+1000)
		return
	}

	// Moderate content
	var inputText string
	for _, msg := range req.Messages {
		inputText += msg.Content + " "
	}

	modResult, err := h.moderator.Moderate(ctx, inputText)
	if err != nil {
		slog.Error("moderation failed", "error", err)
		// Continue anyway, don't block on moderation failure
	} else if modResult.Flagged {
		// Add strike
		banned, err := h.sessionStore.AddStrike(ctx, sess.ID, h.config.MaxStrikes)
		if err != nil {
			slog.Error("failed to add strike", "error", err)
		}

		if banned {
			l402.WriteError(w, http.StatusForbidden, "session_banned", "session has been banned due to policy violations")
		} else {
			l402.WriteError(w, http.StatusBadRequest, "content_violation", "content flagged for: "+modResult.Reason)
		}
		return
	}

	// Get provider
	prov, err := h.providerRouter.GetProvider(req.Model)
	if err != nil {
		l402.WriteError(w, http.StatusBadRequest, "invalid_model", "model not supported")
		return
	}

	// Call provider
	resp, err := prov.Chat(ctx, &req)
	if err != nil {
		if openaiErr, ok := err.(*openai.OpenAIError); ok {
			if openaiErr.StatusCode >= 400 && openaiErr.StatusCode < 500 {
				l402.WriteError(w, openaiErr.StatusCode, "provider_error", openaiErr.Message)
				return
			}
		}
		slog.Error("provider error", "error", err)
		l402.WriteError(w, http.StatusBadGateway, "provider_error", "upstream provider error")
		return
	}

	// Calculate actual cost
	usage := billing.Usage{
		PromptTokens:     resp.Usage.PromptTokens,
		CompletionTokens: resp.Usage.CompletionTokens,
		Model:            req.Model,
	}
	cost, err := h.billing.Calculate(usage)
	if err != nil {
		slog.Error("billing calculation failed", "error", err)
		l402.WriteError(w, http.StatusInternalServerError, "internal_error", "billing error")
		return
	}

	// Debit balance
	reference := req.Model + ":" + resp.ID
	if err := h.sessionStore.DebitBalance(ctx, sess.ID, cost.TotalSats, reference); err != nil {
		slog.Error("failed to debit balance", "error", err)
		l402.WriteError(w, http.StatusInternalServerError, "internal_error", "billing error")
		return
	}

	// Log usage
	usageLog := &session.UsageLog{
		SessionID:        sess.ID,
		Model:            req.Model,
		PromptTokens:     resp.Usage.PromptTokens,
		CompletionTokens: resp.Usage.CompletionTokens,
		CostUSD:          cost.TotalUSD,
		CostSats:         cost.TotalSats,
	}
	if err := h.sessionStore.LogUsage(ctx, usageLog); err != nil {
		slog.Error("failed to log usage", "error", err)
		// Don't fail the request for logging errors
	}

	// Update last used
	if err := h.sessionStore.UpdateLastUsed(ctx, sess.ID); err != nil {
		slog.Error("failed to update last used", "error", err)
	}

	// Return response
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Satilligence-Cost-Sats", fmt.Sprintf("%d", cost.TotalSats))
	json.NewEncoder(w).Encode(resp)
}

func (h *Handler) GetBalance(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	sess := getSessionFromContext(ctx)
	if sess == nil {
		l402.WriteError(w, http.StatusInternalServerError, "internal_error", "session not found")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"balance_sats": sess.BalanceSats,
		"session_id":   sess.ID,
	})
}

func (h *Handler) CreateInvoice(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	sess := getSessionFromContext(ctx)
	if sess == nil {
		l402.WriteError(w, http.StatusInternalServerError, "internal_error", "session not found")
		return
	}

	var req struct {
		AmountSats int64 `json:"amount_sats"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		req.AmountSats = 5000 // Default
	}

	if req.AmountSats < 1000 {
		req.AmountSats = 1000 // Minimum
	}

	invoice, err := h.l402Service.CreateTopUpInvoice(ctx, sess.ID.String(), req.AmountSats)
	if err != nil {
		slog.Error("failed to create invoice", "error", err)
		l402.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to create invoice")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"payment_request": invoice.PaymentRequest,
		"payment_hash":    invoice.PaymentHash,
		"amount_sats":     req.AmountSats,
	})
}

func (h *Handler) GetUsage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	sess := getSessionFromContext(ctx)
	if sess == nil {
		l402.WriteError(w, http.StatusInternalServerError, "internal_error", "session not found")
		return
	}

	// For now, return basic info. Can be extended to return usage history.
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"session_id":   sess.ID,
		"balance_sats": sess.BalanceSats,
		"created_at":   sess.CreatedAt,
		"last_used_at": sess.LastUsedAt,
	})
}

func (h *Handler) ListModels(w http.ResponseWriter, r *http.Request) {
	models := h.providerRouter.ListModels()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"models": models,
	})
}

// Context helpers
type contextKey string

const sessionContextKey contextKey = "session"

func setSessionInContext(r *http.Request, sess *session.Session) *http.Request {
	ctx := r.Context()
	return r.WithContext(context.WithValue(ctx, sessionContextKey, sess))
}

func getSessionFromContext(ctx context.Context) *session.Session {
	sess, _ := ctx.Value(sessionContextKey).(*session.Session)
	return sess
}
