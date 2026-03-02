package api

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/trandor/trandor/config"
	"github.com/trandor/trandor/internal/billing"
	"github.com/trandor/trandor/internal/blink"
	"github.com/trandor/trandor/internal/l402"
	"github.com/trandor/trandor/internal/models"
	"github.com/trandor/trandor/internal/nwc"
	"github.com/trandor/trandor/internal/provider"
	"github.com/trandor/trandor/internal/provider/openai"
)

// NWCHandler handles seamless pay-per-request via NWC
type NWCHandler struct {
	providerRouter *provider.Router
	billing        *billing.Calculator
	blinkClient    *blink.Client
	moderator      *openai.Provider
	modelFeed      *models.ModelFeed
	config         *config.Config
}

func NewNWCHandler(
	providerRouter *provider.Router,
	billing *billing.Calculator,
	blinkClient *blink.Client,
	moderator *openai.Provider,
	modelFeed *models.ModelFeed,
	cfg *config.Config,
) *NWCHandler {
	return &NWCHandler{
		providerRouter: providerRouter,
		billing:        billing,
		blinkClient:    blinkClient,
		moderator:      moderator,
		modelFeed:      modelFeed,
		config:         cfg,
	}
}

// ChatCompletions handles chat requests with NWC auto-payment
// Flow:
// 1. Estimate cost based on input + max output
// 2. Charge 2x estimated cost
// 3. Call AI provider
// 4. Calculate actual cost
// 5. Refund difference immediately via NWC
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
	if !h.modelFeed.IsSupported(req.Model) {
		l402.WriteError(w, http.StatusBadRequest, "invalid_model", "Model '"+req.Model+"' is not supported. Use /v1/models to see available models.")
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

	// Step 1: Estimate cost based on input + max output
	estimatedCost := h.billing.EstimateCost(req.Model, inputText, req.MaxTokens)

	// Step 2: Charge 2x estimated cost
	chargeAmount := estimatedCost * 2
	if chargeAmount < 1 {
		chargeAmount = 1
	}

	slog.Info("charging for request",
		"model", req.Model,
		"max_tokens", req.MaxTokens,
		"estimated_sats", estimatedCost,
		"charge_amount_2x", chargeAmount,
	)

	// Create invoice for 2x amount
	invoice, err := h.blinkClient.CreateInvoice(ctx, chargeAmount, fmt.Sprintf("Trandor: %s request", req.Model))
	if err != nil {
		slog.Error("failed to create invoice", "error", err)
		l402.WriteError(w, http.StatusInternalServerError, "invoice_error", "failed to create payment invoice")
		return
	}

	slog.Info("invoice created", "amount", chargeAmount, "hash", invoice.PaymentHash)

	// Charge via NWC
	preimage, err := nwcClient.PayInvoice(ctx, invoice.PaymentRequest, 30*time.Second)
	if err != nil {
		slog.Error("NWC payment failed", "error", err)
		l402.WriteError(w, http.StatusPaymentRequired, "payment_failed", "NWC payment failed: "+err.Error())
		return
	}

	slog.Info("payment received", "preimage", preimage, "amount", chargeAmount)

	// Step 3: Call AI provider
	prov, err := h.providerRouter.GetProvider(req.Model)
	if err != nil {
		l402.WriteError(w, http.StatusBadRequest, "invalid_model", "model not supported")
		return
	}

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

	// Step 4: Calculate actual cost
	usage := billing.Usage{
		PromptTokens:     resp.Usage.PromptTokens,
		CompletionTokens: resp.Usage.CompletionTokens,
		Model:            req.Model,
	}
	actualCost, _ := h.billing.Calculate(usage)

	// Step 5: Calculate and send refund
	refundAmount := chargeAmount - actualCost.TotalSats
	var refundStatus string

	if refundAmount > 0 {
		slog.Info("processing refund",
			"charged", chargeAmount,
			"actual_cost", actualCost.TotalSats,
			"refund", refundAmount,
		)

		// Ask user's wallet to create invoice for refund
		refundInvoice, err := nwcClient.MakeInvoice(ctx, refundAmount, "Trandor refund", 30*time.Second)
		if err != nil {
			slog.Error("failed to create refund invoice", "error", err)
			refundStatus = "failed: " + err.Error()
		} else {
			// Pay the refund invoice from our Blink wallet
			_, err = h.blinkClient.PayInvoice(ctx, refundInvoice)
			if err != nil {
				slog.Error("failed to pay refund", "error", err)
				refundStatus = "failed: " + err.Error()
			} else {
				slog.Info("refund sent", "amount", refundAmount)
				refundStatus = "success"
			}
		}
	} else {
		refundStatus = "none"
	}

	slog.Info("request completed",
		"model", req.Model,
		"prompt_tokens", resp.Usage.PromptTokens,
		"completion_tokens", resp.Usage.CompletionTokens,
		"cost_usd", fmt.Sprintf("$%.8f", actualCost.TotalUSD),
		"charged_sats", chargeAmount,
		"actual_cost_sats", actualCost.TotalSats,
		"refund_sats", refundAmount,
		"refund_status", refundStatus,
	)

	// Return response with cost info
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Charged-Sats", fmt.Sprintf("%d", chargeAmount))
	w.Header().Set("X-Cost-Sats", fmt.Sprintf("%d", actualCost.TotalSats))
	w.Header().Set("X-Refund-Sats", fmt.Sprintf("%d", refundAmount))
	w.Header().Set("X-Refund-Status", refundStatus)
	json.NewEncoder(w).Encode(resp)
}
