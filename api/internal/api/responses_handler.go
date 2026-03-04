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
	"github.com/trandor/trandor/internal/models"
	"github.com/trandor/trandor/internal/nwc"
	"github.com/trandor/trandor/internal/payment"
	"github.com/trandor/trandor/internal/provider"
	"github.com/trandor/trandor/internal/provider/openai"
)

// ResponsesHandler handles the OpenAI Responses API with NWC payment
type ResponsesHandler struct {
	openaiProvider *openai.Provider
	billing        *billing.Calculator
	charger        *payment.Charger
	modelFeed      *models.ModelFeed
	config         *config.Config
	rateLimiter    *WalletRateLimiter
	blacklist      *WalletBlacklist
}

func NewResponsesHandler(
	openaiProvider *openai.Provider,
	billing *billing.Calculator,
	charger *payment.Charger,
	modelFeed *models.ModelFeed,
	cfg *config.Config,
	rateLimiter *WalletRateLimiter,
	blacklist *WalletBlacklist,
) *ResponsesHandler {
	return &ResponsesHandler{
		openaiProvider: openaiProvider,
		billing:        billing,
		charger:        charger,
		modelFeed:      modelFeed,
		config:         cfg,
		rateLimiter:    rateLimiter,
		blacklist:      blacklist,
	}
}

// Responses handles POST /v1/responses with NWC payment
// Flow:
// 1. Check blacklist
// 2. Rate limit check per wallet pubkey
// 3. Check balance >= minimum ($0.50)
// 4. Call OpenAI Responses API
// 5. Post-charge actual cost (blacklist wallet if payment fails)
func (h *ResponsesHandler) Responses(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Extract NWC connection string from Authorization header
	nwcURL, err := extractNWCFromAuth(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", err.Error())
		return
	}

	// Parse NWC connection
	nwcClient, err := nwc.NewClient(nwcURL)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "invalid NWC connection URL: "+err.Error())
		return
	}
	defer nwcClient.Close()

	// Check blacklist
	walletPubkey := nwcClient.WalletPubkey()
	if h.blacklist != nil && h.blacklist.IsBlacklisted(walletPubkey) {
		slog.Info("blacklisted wallet rejected", "wallet_pubkey", walletPubkey)
		writeError(w, http.StatusForbidden, "blacklisted", "Wallet is blacklisted due to previous payment failures")
		return
	}

	// Rate limit check per wallet pubkey
	if err := h.rateLimiter.Acquire(walletPubkey); err != nil {
		slog.Info("rate limit exceeded", "wallet_pubkey", walletPubkey)
		writeError(w, http.StatusTooManyRequests, "rate_limited", "Too many concurrent requests. Please wait for previous requests to complete.")
		return
	}
	defer h.rateLimiter.Release(walletPubkey)

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

	// Check wallet balance >= minimum requirement ($0.50)
	minBalanceSats := h.billing.USDToSats(h.config.MinBalanceUSD)
	balanceResult := CheckMinBalance(ctx, nwcClient, minBalanceSats, 10*time.Second)

	if balanceResult.SkippedCheck {
		// Wallet doesn't support get_balance - proceed with post-charge
		// If payment fails, wallet will be blacklisted
		slog.Info("wallet doesn't support balance check, proceeding with post-charge",
			"wallet_pubkey", walletPubkey,
		)
	} else if !balanceResult.OK {
		writeError(w, http.StatusPaymentRequired, "insufficient_balance",
			fmt.Sprintf("Insufficient balance: have %d sats, need %d sats minimum",
				balanceResult.BalanceSats, minBalanceSats))
		return
	}

	slog.Info("processing responses request",
		"model", req.Model,
		"max_output_tokens", req.MaxOutputTokens,
		"balance_sats", balanceResult.BalanceSats,
	)

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

	// Post-charge actual cost
	chargeResult := h.charger.PostCharge(ctx, nwcClient, actualCost, payment.FormatDescription(req.Model, "responses"))

	slog.Info("responses request completed",
		"model", req.Model,
		"input_tokens", resp.Usage.InputTokens,
		"output_tokens", resp.Usage.OutputTokens,
		"cost_usd", fmt.Sprintf("$%.8f", actualCost.TotalUSD),
		"cost_sats", actualCost.TotalSats,
		"charge_status", chargeResult.Status,
	)

	// Return response with cost headers
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Cost-Sats", fmt.Sprintf("%d", actualCost.TotalSats))
	w.Header().Set("X-Cost-USD", fmt.Sprintf("%.6f", actualCost.TotalUSD))
	w.Header().Set("X-Charge-Status", string(chargeResult.Status))
	json.NewEncoder(w).Encode(resp)
}

// ResponsesStream handles streaming POST /v1/responses with NWC payment
// Flow:
// 1. Rate limit check per wallet pubkey
// 2. Check balance >= estimated max cost
// 3. Stream OpenAI Responses API
// 4. Post-charge actual cost (accept small losses if payment fails)
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
	sendSSELocal := func(event string, data interface{}) {
		jsonData, _ := json.Marshal(data)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, jsonData)
		flusher.Flush()
	}

	sendError := func(code string, message string) {
		sendSSELocal("error", map[string]string{
			"code":    code,
			"message": message,
		})
	}

	// Extract NWC from Authorization header
	nwcURL, err := extractNWCFromAuth(r)
	if err != nil {
		sendError("unauthorized", err.Error())
		return
	}

	nwcClient, err := nwc.NewClient(nwcURL)
	if err != nil {
		sendError("invalid_credentials", "Invalid NWC connection: "+err.Error())
		return
	}
	defer nwcClient.Close()

	// Check blacklist
	walletPubkey := nwcClient.WalletPubkey()
	if h.blacklist != nil && h.blacklist.IsBlacklisted(walletPubkey) {
		sendError("blacklisted", "Wallet is blacklisted due to previous payment failures")
		return
	}

	// Rate limit check
	if err := h.rateLimiter.Acquire(walletPubkey); err != nil {
		sendError("rate_limited", "Too many concurrent requests")
		return
	}
	defer h.rateLimiter.Release(walletPubkey)

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

	// Check wallet balance >= minimum requirement ($0.50)
	minBalanceSats := h.billing.USDToSats(h.config.MinBalanceUSD)
	balanceResult := CheckMinBalance(ctx, nwcClient, minBalanceSats, 10*time.Second)

	if balanceResult.SkippedCheck {
		// Wallet doesn't support get_balance - proceed with post-charge
		slog.Info("wallet doesn't support balance check, proceeding with post-charge",
			"wallet_pubkey", walletPubkey,
		)
	} else if !balanceResult.OK {
		sendError("insufficient_balance", fmt.Sprintf("Need %d sats minimum, have %d", minBalanceSats, balanceResult.BalanceSats))
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

	// Calculate actual cost and post-charge
	var actualCostSats int64 = 0
	var actualCostUSD float64 = 0
	var chargeStatus payment.ChargeStatus = payment.ChargeNoUsage

	if totalInputTokens > 0 || totalOutputTokens > 0 {
		usage := billing.Usage{
			PromptTokens:     totalInputTokens,
			CompletionTokens: totalOutputTokens,
			Model:            req.Model,
		}
		actualCost, _ := h.billing.Calculate(usage)
		actualCostSats = actualCost.TotalSats
		actualCostUSD = actualCost.TotalUSD

		// Post-charge actual cost in background
		chargeResult := h.charger.PostChargeAsync(nwcClient, actualCost, payment.FormatDescription(req.Model, "responses streaming"))
		chargeStatus = chargeResult.Status
	}

	slog.Info("streaming responses request completed",
		"model", req.Model,
		"cost_sats", actualCostSats,
		"cost_usd", actualCostUSD,
		"charge_status", chargeStatus,
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
