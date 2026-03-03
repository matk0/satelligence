package api

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/trandor/trandor/config"
	"github.com/trandor/trandor/internal/billing"
	"github.com/trandor/trandor/internal/blink"
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
		writeError(w, http.StatusBadRequest, "missing_nwc", "X-NWC header required with Nostr Wallet Connect URL")
		return
	}

	// Parse NWC connection
	nwcClient, err := nwc.NewClient(nwcURL)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_nwc", "invalid NWC connection URL: "+err.Error())
		return
	}
	defer nwcClient.Close()

	// Parse request
	var req provider.ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "failed to parse request body")
		return
	}

	// Validate model
	if !h.modelFeed.IsSupported(req.Model) {
		writeError(w, http.StatusBadRequest, "invalid_model", "Model '"+req.Model+"' is not supported. Use /v1/models to see available models.")
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
		writeError(w, http.StatusBadRequest, "content_violation", "content flagged for: "+modResult.Reason)
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
		writeError(w, http.StatusInternalServerError, "invoice_error", "failed to create payment invoice")
		return
	}

	slog.Info("invoice created", "amount", chargeAmount, "hash", invoice.PaymentHash)

	// Charge via NWC
	preimage, err := nwcClient.PayInvoice(ctx, invoice.PaymentRequest, 30*time.Second)
	if err != nil {
		slog.Error("NWC payment failed", "error", err)
		writeError(w, http.StatusPaymentRequired, "payment_failed", "NWC payment failed: "+err.Error())
		return
	}

	slog.Info("payment received", "preimage", preimage, "amount", chargeAmount)

	// Step 3: Call AI provider
	prov, err := h.providerRouter.GetProvider(req.Model)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_model", "model not supported")
		return
	}

	resp, err := prov.Chat(ctx, &req)
	if err != nil {
		if openaiErr, ok := err.(*openai.OpenAIError); ok {
			if openaiErr.StatusCode >= 400 && openaiErr.StatusCode < 500 {
				writeError(w, openaiErr.StatusCode, "provider_error", openaiErr.Message)
				return
			}
		}
		slog.Error("provider error", "error", err)
		writeError(w, http.StatusBadGateway, "provider_error", "upstream provider error")
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
	w.Header().Set("X-Cost-USD", fmt.Sprintf("%.6f", actualCost.TotalUSD))
	w.Header().Set("X-Refund-Sats", fmt.Sprintf("%d", refundAmount))
	w.Header().Set("X-Refund-Status", refundStatus)
	json.NewEncoder(w).Encode(resp)
}

// SSE event types for streaming progress
type StreamEvent struct {
	Event string      `json:"event"`
	Data  interface{} `json:"data"`
}

// sendSSE sends a server-sent event
func sendSSE(w http.ResponseWriter, flusher http.Flusher, event string, data interface{}) {
	jsonData, _ := json.Marshal(data)
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, jsonData)
	flusher.Flush()
}

// ChatCompletionsStream handles streaming chat requests with NWC auto-payment
// Sends SSE events for each step and streams the AI response tokens
// POST /v1/nwc/chat/completions/stream
func (h *NWCHandler) ChatCompletionsStream(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Set up SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	// Helper to send step updates
	sendStep := func(step string, status string, details map[string]interface{}) {
		if details == nil {
			details = make(map[string]interface{})
		}
		details["step"] = step
		details["status"] = status
		sendSSE(w, flusher, "step", details)
	}

	// Helper to send errors
	sendError := func(code string, message string) {
		sendSSE(w, flusher, "error", map[string]string{
			"code":    code,
			"message": message,
		})
	}

	// Step 1: Wallet connection
	sendStep("wallet_connect", "pending", nil)

	nwcURL := r.Header.Get("X-NWC")
	if nwcURL == "" {
		sendStep("wallet_connect", "error", nil)
		sendError("missing_nwc", "X-NWC header required")
		return
	}

	nwcClient, err := nwc.NewClient(nwcURL)
	if err != nil {
		sendStep("wallet_connect", "error", nil)
		sendError("invalid_nwc", "Invalid NWC connection: "+err.Error())
		return
	}
	defer nwcClient.Close()

	sendStep("wallet_connect", "complete", nil)

	// Parse request
	var req provider.ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendError("invalid_request", "Failed to parse request body")
		return
	}

	if !h.modelFeed.IsSupported(req.Model) {
		sendError("invalid_model", "Model '"+req.Model+"' is not supported")
		return
	}

	if req.MaxTokens == 0 {
		req.MaxTokens = 4096
	}

	// Step 2: Content moderation
	sendStep("moderation", "pending", nil)

	var inputText string
	for _, msg := range req.Messages {
		inputText += msg.Content + " "
	}

	modResult, err := h.moderator.Moderate(ctx, inputText)
	if err != nil {
		slog.Error("moderation failed", "error", err)
		// Continue anyway but mark as warning
		sendStep("moderation", "complete", map[string]interface{}{"warning": "moderation unavailable"})
	} else if modResult.Flagged {
		sendStep("moderation", "error", nil)
		sendError("content_violation", "Content flagged: "+modResult.Reason)
		return
	} else {
		sendStep("moderation", "complete", nil)
	}

	// Step 3: Cost estimation
	sendStep("cost_estimate", "pending", nil)

	estimatedCost := h.billing.EstimateCost(req.Model, inputText, req.MaxTokens)
	chargeAmount := estimatedCost * 2
	if chargeAmount < 1 {
		chargeAmount = 1
	}

	sendStep("cost_estimate", "complete", map[string]interface{}{
		"estimated_sats": estimatedCost,
		"charge_sats":    chargeAmount,
	})

	// Step 4: Invoice creation
	sendStep("invoice_create", "pending", nil)

	invoice, err := h.blinkClient.CreateInvoice(ctx, chargeAmount, fmt.Sprintf("Trandor: %s request", req.Model))
	if err != nil {
		slog.Error("failed to create invoice", "error", err)
		sendStep("invoice_create", "error", nil)
		sendError("invoice_error", "Failed to create invoice")
		return
	}

	sendStep("invoice_create", "complete", map[string]interface{}{
		"amount_sats": chargeAmount,
	})

	// Step 5: Payment request
	sendStep("payment_request", "pending", map[string]interface{}{
		"amount_sats": chargeAmount,
	})

	preimage, err := nwcClient.PayInvoice(ctx, invoice.PaymentRequest, 30*time.Second)
	if err != nil {
		slog.Error("NWC payment failed", "error", err)
		sendStep("payment_request", "error", nil)
		sendError("payment_failed", "Payment failed: "+err.Error())
		return
	}

	sendStep("payment_request", "complete", map[string]interface{}{
		"preimage": preimage[:16] + "...", // Truncate for display
	})

	// Step 6: AI generation (streaming)
	sendStep("generation", "pending", nil)

	prov, err := h.providerRouter.GetProvider(req.Model)
	if err != nil {
		sendStep("generation", "error", nil)
		sendError("invalid_model", "Model not supported")
		return
	}

	// Cast to OpenAI provider for streaming
	openaiProv, ok := prov.(*openai.Provider)
	if !ok {
		sendStep("generation", "error", nil)
		sendError("streaming_not_supported", "Provider does not support streaming")
		return
	}

	stream, err := openaiProv.ChatStream(ctx, &req)
	if err != nil {
		if openaiErr, ok := err.(*openai.OpenAIError); ok {
			sendStep("generation", "error", nil)
			sendError("provider_error", openaiErr.Message)
			return
		}
		slog.Error("provider stream error", "error", err)
		sendStep("generation", "error", nil)
		sendError("provider_error", "Upstream provider error")
		return
	}
	defer stream.Close()

	// Stream tokens
	var fullContent strings.Builder
	var usage *provider.ChatUsage

	for {
		line, ok, err := stream.Next()
		if err != nil {
			slog.Error("stream read error", "error", err)
			break
		}
		if !ok {
			break
		}

		if line == "" {
			continue
		}

		chunk, err := openai.ParseStreamChunk(line)
		if err != nil {
			continue
		}

		if chunk != nil {
			// Send token to client
			if len(chunk.Choices) > 0 && chunk.Choices[0].Delta.Content != "" {
				content := chunk.Choices[0].Delta.Content
				fullContent.WriteString(content)
				sendSSE(w, flusher, "token", map[string]string{
					"content": content,
				})
			}
			// Capture usage from final chunk
			if chunk.Usage != nil {
				usage = chunk.Usage
			}
		}
	}

	sendStep("generation", "complete", nil)

	// Step 7: Cost calculation
	sendStep("cost_calculate", "pending", nil)

	var actualCostSats int64 = 0
	var actualCostUSD float64 = 0
	var refundAmount int64 = 0

	if usage != nil {
		usageCalc := billing.Usage{
			PromptTokens:     usage.PromptTokens,
			CompletionTokens: usage.CompletionTokens,
			Model:            req.Model,
		}
		actualCost, _ := h.billing.Calculate(usageCalc)
		actualCostSats = actualCost.TotalSats
		actualCostUSD = actualCost.TotalUSD
		refundAmount = chargeAmount - actualCostSats
		if refundAmount < 0 {
			refundAmount = 0
		}
	}

	sendStep("cost_calculate", "complete", map[string]interface{}{
		"cost_sats":   actualCostSats,
		"cost_usd":    actualCostUSD,
		"refund_sats": refundAmount,
	})

	// Step 8: Refund processing
	if refundAmount > 0 {
		sendStep("refund", "pending", map[string]interface{}{
			"amount_sats": refundAmount,
		})

		refundInvoice, err := nwcClient.MakeInvoice(ctx, refundAmount, "Trandor refund", 30*time.Second)
		if err != nil {
			slog.Error("failed to create refund invoice", "error", err)
			sendStep("refund", "error", map[string]interface{}{
				"error": "Failed to create refund invoice",
			})
		} else {
			_, err = h.blinkClient.PayInvoice(ctx, refundInvoice)
			if err != nil {
				slog.Error("failed to pay refund", "error", err)
				sendStep("refund", "error", map[string]interface{}{
					"error": "Failed to send refund",
				})
			} else {
				sendStep("refund", "complete", map[string]interface{}{
					"amount_sats": refundAmount,
				})
			}
		}
	}

	// Send final summary
	sendSSE(w, flusher, "complete", map[string]interface{}{
		"content":      fullContent.String(),
		"charged_sats": chargeAmount,
		"cost_sats":    actualCostSats,
		"cost_usd":     actualCostUSD,
		"refund_sats":  refundAmount,
		"usage": map[string]interface{}{
			"prompt_tokens":     usage.PromptTokens,
			"completion_tokens": usage.CompletionTokens,
		},
	})

	slog.Info("streaming request completed",
		"model", req.Model,
		"charged_sats", chargeAmount,
		"actual_cost_sats", actualCostSats,
		"refund_sats", refundAmount,
	)
}
