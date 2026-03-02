package api

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/satilligence/satilligence/config"
	"github.com/satilligence/satilligence/internal/billing"
	"github.com/satilligence/satilligence/internal/blink"
	"github.com/satilligence/satilligence/internal/l402"
	"github.com/satilligence/satilligence/internal/provider"
	"github.com/satilligence/satilligence/internal/provider/openai"
)

// WebLNHandler handles browser-based WebLN payments
type WebLNHandler struct {
	providerRouter *provider.Router
	billing        *billing.Calculator
	blinkClient    *blink.Client
	moderator      *openai.Provider
	config         *config.Config

	// Pending quotes (payment_hash -> quote)
	quotes   map[string]*Quote
	quotesMu sync.RWMutex
}

// Quote represents a pending payment quote
type Quote struct {
	PaymentHash    string                 `json:"payment_hash"`
	PaymentRequest string                 `json:"payment_request"`
	AmountSats     int64                  `json:"amount_sats"`
	Model          string                 `json:"model"`
	Messages       []provider.ChatMessage `json:"-"`
	MaxTokens      int                    `json:"-"`
	ExpiresAt      time.Time              `json:"expires_at"`
}

func NewWebLNHandler(
	providerRouter *provider.Router,
	billing *billing.Calculator,
	blinkClient *blink.Client,
	moderator *openai.Provider,
	cfg *config.Config,
) *WebLNHandler {
	h := &WebLNHandler{
		providerRouter: providerRouter,
		billing:        billing,
		blinkClient:    blinkClient,
		moderator:      moderator,
		config:         cfg,
		quotes:         make(map[string]*Quote),
	}

	// Clean up expired quotes periodically
	go h.cleanupExpiredQuotes()

	return h
}

func (h *WebLNHandler) cleanupExpiredQuotes() {
	ticker := time.NewTicker(1 * time.Minute)
	for range ticker.C {
		h.quotesMu.Lock()
		now := time.Now()
		for hash, quote := range h.quotes {
			if now.After(quote.ExpiresAt) {
				delete(h.quotes, hash)
			}
		}
		h.quotesMu.Unlock()
	}
}

// QuoteRequest is the request body for creating a quote
type QuoteRequest struct {
	Model    string                 `json:"model"`
	Messages []provider.ChatMessage `json:"messages"`
	MaxTokens int                   `json:"max_tokens,omitempty"`
}

// QuoteResponse is returned when creating a quote
type QuoteResponse struct {
	PaymentHash    string `json:"payment_hash"`
	PaymentRequest string `json:"payment_request"`
	AmountSats     int64  `json:"amount_sats"`
	ExpiresAt      string `json:"expires_at"`
}

// CreateQuote creates an invoice for a chat request
// POST /v1/webln/quote
func (h *WebLNHandler) CreateQuote(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req QuoteRequest
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

	// Moderate content
	var inputText string
	for _, msg := range req.Messages {
		inputText += msg.Content + " "
	}

	modResult, err := h.moderator.Moderate(ctx, inputText)
	if err != nil {
		slog.Error("moderation failed", "error", err)
	} else if modResult.Flagged {
		l402.WriteError(w, http.StatusBadRequest, "content_violation", "content flagged for: "+modResult.Reason)
		return
	}

	// Estimate cost with 2x safety buffer
	estimatedCost := h.billing.EstimateCost(req.Model, inputText, req.MaxTokens)
	chargeAmount := estimatedCost * 2
	if chargeAmount < 1 {
		chargeAmount = 1
	}

	// Create invoice
	invoice, err := h.blinkClient.CreateInvoice(ctx, chargeAmount, fmt.Sprintf("Satilligence: %s request", req.Model))
	if err != nil {
		slog.Error("failed to create invoice", "error", err)
		l402.WriteError(w, http.StatusInternalServerError, "invoice_error", "failed to create payment invoice")
		return
	}

	// Store quote for later retrieval
	expiresAt := time.Now().Add(10 * time.Minute)
	quote := &Quote{
		PaymentHash:    invoice.PaymentHash,
		PaymentRequest: invoice.PaymentRequest,
		AmountSats:     chargeAmount,
		Model:          req.Model,
		Messages:       req.Messages,
		MaxTokens:      req.MaxTokens,
		ExpiresAt:      expiresAt,
	}

	h.quotesMu.Lock()
	h.quotes[invoice.PaymentHash] = quote
	h.quotesMu.Unlock()

	slog.Info("quote created",
		"payment_hash", invoice.PaymentHash,
		"amount", chargeAmount,
		"model", req.Model,
	)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(QuoteResponse{
		PaymentHash:    invoice.PaymentHash,
		PaymentRequest: invoice.PaymentRequest,
		AmountSats:     chargeAmount,
		ExpiresAt:      expiresAt.Format(time.RFC3339),
	})
}

// ChatCompletions handles chat requests after WebLN payment
// POST /v1/webln/chat/completions
// Headers: X-Payment-Hash (required)
func (h *WebLNHandler) ChatCompletions(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Get payment hash from header
	paymentHash := r.Header.Get("X-Payment-Hash")
	if paymentHash == "" {
		l402.WriteError(w, http.StatusBadRequest, "missing_payment", "X-Payment-Hash header required")
		return
	}

	// Look up the quote
	h.quotesMu.Lock()
	quote, exists := h.quotes[paymentHash]
	if exists {
		delete(h.quotes, paymentHash) // One-time use
	}
	h.quotesMu.Unlock()

	if !exists {
		l402.WriteError(w, http.StatusBadRequest, "invalid_quote", "quote not found or expired")
		return
	}

	if time.Now().After(quote.ExpiresAt) {
		l402.WriteError(w, http.StatusBadRequest, "expired_quote", "quote has expired")
		return
	}

	// Verify payment was received
	status, err := h.blinkClient.GetInvoiceStatus(ctx, paymentHash)
	if err != nil {
		slog.Error("failed to check invoice status", "error", err)
		l402.WriteError(w, http.StatusInternalServerError, "payment_check_error", "failed to verify payment")
		return
	}

	if status != blink.InvoiceStatusPaid {
		l402.WriteError(w, http.StatusPaymentRequired, "payment_required", "invoice not paid yet")
		return
	}

	slog.Info("payment verified", "payment_hash", paymentHash)

	// Get provider
	prov, err := h.providerRouter.GetProvider(quote.Model)
	if err != nil {
		l402.WriteError(w, http.StatusBadRequest, "invalid_model", "model not supported")
		return
	}

	// Call provider
	req := &provider.ChatRequest{
		Model:     quote.Model,
		Messages:  quote.Messages,
		MaxTokens: quote.MaxTokens,
	}

	resp, err := prov.Chat(ctx, req)
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
		Model:            quote.Model,
	}
	actualCost, _ := h.billing.Calculate(usage)

	// Calculate refund
	refundAmount := quote.AmountSats - actualCost.TotalSats
	if refundAmount < 0 {
		refundAmount = 0
	}

	slog.Info("webln request completed",
		"model", quote.Model,
		"prompt_tokens", resp.Usage.PromptTokens,
		"completion_tokens", resp.Usage.CompletionTokens,
		"charged_sats", quote.AmountSats,
		"actual_cost_sats", actualCost.TotalSats,
		"refund_sats", refundAmount,
	)

	// Return response with refund amount in header
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Charged-Sats", fmt.Sprintf("%d", quote.AmountSats))
	w.Header().Set("X-Cost-Sats", fmt.Sprintf("%d", actualCost.TotalSats))
	w.Header().Set("X-Refund-Sats", fmt.Sprintf("%d", refundAmount))
	json.NewEncoder(w).Encode(resp)
}

// RefundRequest is the request body for claiming a refund
type RefundRequest struct {
	PaymentRequest string `json:"payment_request"`
}

// Refund pays a user-provided invoice for their refund
// POST /v1/webln/refund
func (h *WebLNHandler) Refund(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req RefundRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		l402.WriteError(w, http.StatusBadRequest, "invalid_request", "failed to parse request body")
		return
	}

	if req.PaymentRequest == "" {
		l402.WriteError(w, http.StatusBadRequest, "missing_invoice", "payment_request is required")
		return
	}

	// Pay the refund invoice
	result, err := h.blinkClient.PayInvoice(ctx, req.PaymentRequest)
	if err != nil {
		slog.Error("failed to pay refund invoice", "error", err)
		l402.WriteError(w, http.StatusInternalServerError, "refund_error", "failed to process refund")
		return
	}

	slog.Info("refund paid", "status", result.Status)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status": "refunded",
	})
}
