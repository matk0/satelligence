package api

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/satilligence/satilligence/config"
	"github.com/satilligence/satilligence/internal/billing"
	"github.com/satilligence/satilligence/internal/blink"
	"github.com/satilligence/satilligence/internal/l402"
	"github.com/satilligence/satilligence/internal/nwc"
	"github.com/satilligence/satilligence/internal/provider"
	"github.com/satilligence/satilligence/internal/provider/openai"
)

// NWCHandler handles seamless pay-per-request via NWC
type NWCHandler struct {
	providerRouter *provider.Router
	billing        *billing.Calculator
	blinkClient    *blink.Client
	moderator      *openai.Provider
	config         *config.Config
}

func NewNWCHandler(
	providerRouter *provider.Router,
	billing *billing.Calculator,
	blinkClient *blink.Client,
	moderator *openai.Provider,
	cfg *config.Config,
) *NWCHandler {
	return &NWCHandler{
		providerRouter: providerRouter,
		billing:        billing,
		blinkClient:    blinkClient,
		moderator:      moderator,
		config:         cfg,
	}
}

// ChatCompletions handles chat requests with NWC auto-payment
func (h *NWCHandler) ChatCompletions(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Get NWC connection string from header
	nwcURL := r.Header.Get("X-NWC")
	if nwcURL == "" {
		l402.WriteError(w, http.StatusBadRequest, "missing_nwc", "X-NWC header required with Nostr Wallet Connect URL")
		return
	}

	// Parse NWC connection
	nwcClient, err := nwc.NewClient(nwcURL)
	if err != nil {
		l402.WriteError(w, http.StatusBadRequest, "invalid_nwc", "invalid NWC connection URL: "+err.Error())
		return
	}
	defer nwcClient.Close()

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

	// Moderate content
	var inputText string
	for _, msg := range req.Messages {
		inputText += msg.Content + " "
	}

	modResult, err := h.moderator.Moderate(ctx, inputText)
	if err != nil {
		slog.Error("moderation failed", "error", err)
		// Continue anyway
	} else if modResult.Flagged {
		l402.WriteError(w, http.StatusBadRequest, "content_violation", "content flagged for: "+modResult.Reason)
		return
	}

	// Estimate cost for this request
	estimatedCost := h.billing.EstimateMaxCost(req.Model, req.MaxTokens)

	// Minimum 10 sats per request
	if estimatedCost < 10 {
		estimatedCost = 10
	}

	slog.Info("estimated cost", "sats", estimatedCost, "model", req.Model, "max_tokens", req.MaxTokens)

	// Create invoice for this request
	invoice, err := h.blinkClient.CreateInvoice(ctx, estimatedCost, fmt.Sprintf("Satilligence: %s request", req.Model))
	if err != nil {
		slog.Error("failed to create invoice", "error", err)
		l402.WriteError(w, http.StatusInternalServerError, "invoice_error", "failed to create payment invoice")
		return
	}

	slog.Info("invoice created", "amount", estimatedCost, "hash", invoice.PaymentHash)

	// Pay via NWC
	preimage, err := nwcClient.PayInvoice(ctx, invoice.PaymentRequest, 30*time.Second)
	if err != nil {
		slog.Error("NWC payment failed", "error", err)
		l402.WriteError(w, http.StatusPaymentRequired, "payment_failed", "NWC payment failed: "+err.Error())
		return
	}

	slog.Info("payment received", "preimage", preimage)

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
	actualCost, _ := h.billing.Calculate(usage)

	slog.Info("request completed",
		"model", req.Model,
		"prompt_tokens", resp.Usage.PromptTokens,
		"completion_tokens", resp.Usage.CompletionTokens,
		"estimated_sats", estimatedCost,
		"actual_sats", actualCost.TotalSats,
	)

	// Return response with cost info
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Cost-Sats", fmt.Sprintf("%d", actualCost.TotalSats))
	w.Header().Set("X-Paid-Sats", fmt.Sprintf("%d", estimatedCost))
	json.NewEncoder(w).Encode(resp)
}
