package api

import (
	"context"
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

// ResponsesHandler handles the OpenAI Responses API with NWC payment
type ResponsesHandler struct {
	openaiProvider *openai.Provider
	billing        *billing.Calculator
	blinkClient    *blink.Client
	modelFeed      *models.ModelFeed
	config         *config.Config
}

func NewResponsesHandler(
	openaiProvider *openai.Provider,
	billing *billing.Calculator,
	blinkClient *blink.Client,
	modelFeed *models.ModelFeed,
	cfg *config.Config,
) *ResponsesHandler {
	return &ResponsesHandler{
		openaiProvider: openaiProvider,
		billing:        billing,
		blinkClient:    blinkClient,
		modelFeed:      modelFeed,
		config:         cfg,
	}
}

// Responses handles POST /v1/responses with NWC payment
func (h *ResponsesHandler) Responses(w http.ResponseWriter, r *http.Request) {
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
	var req provider.ResponsesRequest
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
	if req.MaxOutputTokens == 0 {
		req.MaxOutputTokens = 4096
	}

	// Extract input text for moderation and cost estimation
	inputText := extractInputText(req.Input)
	if req.Instructions != "" {
		inputText = req.Instructions + " " + inputText
	}

	// Moderate content
	modResult, err := h.openaiProvider.Moderate(ctx, inputText)
	if err != nil {
		slog.Error("moderation failed", "error", err)
		// Continue anyway
	} else if modResult.Flagged {
		writeError(w, http.StatusBadRequest, "content_violation", "content flagged for: "+modResult.Reason)
		return
	}

	// Estimate cost
	estimatedCost := h.billing.EstimateCost(req.Model, inputText, req.MaxOutputTokens)

	// Charge 2x estimated cost
	chargeAmount := estimatedCost * 2
	if chargeAmount < 1 {
		chargeAmount = 1
	}

	slog.Info("charging for responses request",
		"model", req.Model,
		"max_output_tokens", req.MaxOutputTokens,
		"estimated_sats", estimatedCost,
		"charge_amount_2x", chargeAmount,
	)

	// Create invoice
	invoice, err := h.blinkClient.CreateInvoice(ctx, chargeAmount, fmt.Sprintf("Trandor: %s responses", req.Model))
	if err != nil {
		slog.Error("failed to create invoice", "error", err)
		writeError(w, http.StatusInternalServerError, "invoice_error", "failed to create payment invoice")
		return
	}

	// Charge via NWC
	preimage, err := nwcClient.PayInvoice(ctx, invoice.PaymentRequest, 30*time.Second)
	if err != nil {
		slog.Error("NWC payment failed", "error", err)
		writeError(w, http.StatusPaymentRequired, "payment_failed", "NWC payment failed: "+err.Error())
		return
	}

	slog.Info("payment received", "preimage", preimage, "amount", chargeAmount)

	// Call OpenAI Responses API
	resp, err := h.openaiProvider.Responses(ctx, &req)
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

	// Calculate actual cost from usage
	usage := billing.Usage{
		PromptTokens:     resp.Usage.InputTokens,
		CompletionTokens: resp.Usage.OutputTokens,
		Model:            req.Model,
	}
	actualCost, _ := h.billing.Calculate(usage)

	// Calculate and send refund
	refundAmount := chargeAmount - actualCost.TotalSats
	var refundStatus string

	if refundAmount > 0 {
		slog.Info("processing refund",
			"charged", chargeAmount,
			"actual_cost", actualCost.TotalSats,
			"refund", refundAmount,
		)

		refundInvoice, err := nwcClient.MakeInvoice(ctx, refundAmount, "Trandor refund", 30*time.Second)
		if err != nil {
			slog.Error("failed to create refund invoice", "error", err)
			refundStatus = "failed: " + err.Error()
		} else {
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

	slog.Info("responses request completed",
		"model", req.Model,
		"input_tokens", resp.Usage.InputTokens,
		"output_tokens", resp.Usage.OutputTokens,
		"cost_usd", fmt.Sprintf("$%.8f", actualCost.TotalUSD),
		"charged_sats", chargeAmount,
		"actual_cost_sats", actualCost.TotalSats,
		"refund_sats", refundAmount,
		"refund_status", refundStatus,
	)

	// Return response with cost headers
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Charged-Sats", fmt.Sprintf("%d", chargeAmount))
	w.Header().Set("X-Cost-Sats", fmt.Sprintf("%d", actualCost.TotalSats))
	w.Header().Set("X-Cost-USD", fmt.Sprintf("%.6f", actualCost.TotalUSD))
	w.Header().Set("X-Refund-Sats", fmt.Sprintf("%d", refundAmount))
	w.Header().Set("X-Refund-Status", refundStatus)
	json.NewEncoder(w).Encode(resp)
}

// ResponsesStream handles streaming POST /v1/responses with NWC payment
func (h *ResponsesHandler) ResponsesStream(w http.ResponseWriter, r *http.Request) {
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

	// Helper to send SSE events
	sendSSE := func(event string, data interface{}) {
		jsonData, _ := json.Marshal(data)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, jsonData)
		flusher.Flush()
	}

	sendError := func(code string, message string) {
		sendSSE("error", map[string]string{
			"code":    code,
			"message": message,
		})
	}

	// Get NWC connection
	nwcURL := r.Header.Get("X-NWC")
	if nwcURL == "" {
		sendError("missing_nwc", "X-NWC header required")
		return
	}

	nwcClient, err := nwc.NewClient(nwcURL)
	if err != nil {
		sendError("invalid_nwc", "Invalid NWC connection: "+err.Error())
		return
	}
	defer nwcClient.Close()

	// Parse request
	var req provider.ResponsesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendError("invalid_request", "Failed to parse request body")
		return
	}

	if !h.modelFeed.IsSupported(req.Model) {
		sendError("invalid_model", "Model '"+req.Model+"' is not supported")
		return
	}

	if req.MaxOutputTokens == 0 {
		req.MaxOutputTokens = 4096
	}

	// Extract input and moderate
	inputText := extractInputText(req.Input)
	if req.Instructions != "" {
		inputText = req.Instructions + " " + inputText
	}

	modResult, err := h.openaiProvider.Moderate(ctx, inputText)
	if err != nil {
		slog.Error("moderation failed", "error", err)
	} else if modResult.Flagged {
		sendError("content_violation", "Content flagged: "+modResult.Reason)
		return
	}

	// Estimate and charge
	estimatedCost := h.billing.EstimateCost(req.Model, inputText, req.MaxOutputTokens)
	chargeAmount := estimatedCost * 2
	if chargeAmount < 1 {
		chargeAmount = 1
	}

	invoice, err := h.blinkClient.CreateInvoice(ctx, chargeAmount, fmt.Sprintf("Trandor: %s responses", req.Model))
	if err != nil {
		slog.Error("failed to create invoice", "error", err)
		sendError("invoice_error", "Failed to create invoice")
		return
	}

	_, err = nwcClient.PayInvoice(ctx, invoice.PaymentRequest, 30*time.Second)
	if err != nil {
		slog.Error("NWC payment failed", "error", err)
		sendError("payment_failed", "Payment failed: "+err.Error())
		return
	}

	// Call streaming Responses API
	stream, err := h.openaiProvider.ResponsesStream(ctx, &req)
	if err != nil {
		if openaiErr, ok := err.(*openai.OpenAIError); ok {
			sendError("provider_error", openaiErr.Message)
			return
		}
		slog.Error("provider stream error", "error", err)
		sendError("provider_error", "Upstream provider error")
		return
	}
	defer stream.Close()

	// Forward stream events to client
	var totalInputTokens, totalOutputTokens int
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

		// Forward SSE lines directly
		if strings.HasPrefix(line, "data: ") {
			data := strings.TrimPrefix(line, "data: ")
			if data == "[DONE]" {
				fmt.Fprintf(w, "data: [DONE]\n\n")
				flusher.Flush()
				break
			}

			// Try to extract usage from the event
			var event map[string]interface{}
			if err := json.Unmarshal([]byte(data), &event); err == nil {
				if usage, ok := event["usage"].(map[string]interface{}); ok {
					if input, ok := usage["input_tokens"].(float64); ok {
						totalInputTokens = int(input)
					}
					if output, ok := usage["output_tokens"].(float64); ok {
						totalOutputTokens = int(output)
					}
				}
			}

			fmt.Fprintf(w, "%s\n\n", line)
			flusher.Flush()
		} else if strings.HasPrefix(line, "event: ") {
			fmt.Fprintf(w, "%s\n", line)
			flusher.Flush()
		}
	}

	// Calculate actual cost and process refund
	var actualCostSats int64 = 0
	var actualCostUSD float64 = 0
	var refundAmount int64 = 0

	if totalInputTokens > 0 || totalOutputTokens > 0 {
		usage := billing.Usage{
			PromptTokens:     totalInputTokens,
			CompletionTokens: totalOutputTokens,
			Model:            req.Model,
		}
		actualCost, _ := h.billing.Calculate(usage)
		actualCostSats = actualCost.TotalSats
		actualCostUSD = actualCost.TotalUSD
		refundAmount = chargeAmount - actualCostSats
		if refundAmount < 0 {
			refundAmount = 0
		}
	}

	// Process refund in background (don't block response)
	if refundAmount > 0 {
		go func() {
			refundInvoice, err := nwcClient.MakeInvoice(context.Background(), refundAmount, "Trandor refund", 30*time.Second)
			if err != nil {
				slog.Error("failed to create refund invoice", "error", err)
				return
			}
			_, err = h.blinkClient.PayInvoice(context.Background(), refundInvoice)
			if err != nil {
				slog.Error("failed to pay refund", "error", err)
				return
			}
			slog.Info("refund sent", "amount", refundAmount)
		}()
	}

	slog.Info("streaming responses request completed",
		"model", req.Model,
		"charged_sats", chargeAmount,
		"actual_cost_sats", actualCostSats,
		"actual_cost_usd", actualCostUSD,
		"refund_sats", refundAmount,
	)
}

// extractInputText extracts text from the input field which can be string or array
func extractInputText(input interface{}) string {
	if input == nil {
		return ""
	}

	// If it's a string, return directly
	if str, ok := input.(string); ok {
		return str
	}

	// If it's an array of messages, extract content
	if arr, ok := input.([]interface{}); ok {
		var parts []string
		for _, item := range arr {
			if msg, ok := item.(map[string]interface{}); ok {
				if content, ok := msg["content"].(string); ok {
					parts = append(parts, content)
				}
			}
		}
		return strings.Join(parts, " ")
	}

	return ""
}
