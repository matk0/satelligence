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

// NWCHandler handles seamless pay-per-request via NWC
type NWCHandler struct {
	providerRouter *provider.Router
	billing        *billing.Calculator
	blinkClient    *blink.Client
	moderator      *openai.Provider
	modelFeed      *models.ModelFeed
	config         *config.Config
	rateLimiter    *WalletRateLimiter
}

func NewNWCHandler(
	providerRouter *provider.Router,
	billing *billing.Calculator,
	blinkClient *blink.Client,
	moderator *openai.Provider,
	modelFeed *models.ModelFeed,
	cfg *config.Config,
	rateLimiter *WalletRateLimiter,
) *NWCHandler {
	return &NWCHandler{
		providerRouter: providerRouter,
		billing:        billing,
		blinkClient:    blinkClient,
		moderator:      moderator,
		modelFeed:      modelFeed,
		config:         cfg,
		rateLimiter:    rateLimiter,
	}
}

// extractNWCFromAuth extracts the NWC connection string from the Authorization header.
// Expected format: "Authorization: Bearer nostr+walletconnect://..."
func extractNWCFromAuth(r *http.Request) (string, error) {
	auth := r.Header.Get("Authorization")
	if auth == "" {
		return "", fmt.Errorf("missing Authorization header")
	}

	if !strings.HasPrefix(auth, "Bearer ") {
		return "", fmt.Errorf("Authorization header must use Bearer scheme")
	}

	token := strings.TrimPrefix(auth, "Bearer ")
	if !strings.HasPrefix(token, "nostr+walletconnect://") {
		return "", fmt.Errorf("Bearer token must be a nostr+walletconnect:// URL")
	}

	return token, nil
}

// ChatCompletions handles chat requests with NWC auto-payment
// Flow:
// 1. Rate limit check per wallet pubkey
// 2. Check balance >= estimated max cost
// 3. Call AI provider
// 4. Post-charge actual cost (accept small losses if payment fails)
func (h *NWCHandler) ChatCompletions(w http.ResponseWriter, r *http.Request) {
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

	// Rate limit check per wallet pubkey
	walletPubkey := nwcClient.WalletPubkey()
	if err := h.rateLimiter.Acquire(walletPubkey); err != nil {
		slog.Info("rate limit exceeded", "wallet_pubkey", walletPubkey)
		writeError(w, http.StatusTooManyRequests, "rate_limited", "Too many concurrent requests. Please wait for previous requests to complete.")
		return
	}
	defer h.rateLimiter.Release(walletPubkey)

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
		inputText += msg.Content.String() + " "
	}

	modResult, err := h.moderator.Moderate(ctx, inputText)
	if err != nil {
		slog.Error("moderation failed", "error", err)
		// Continue anyway
	} else if modResult.Flagged {
		writeError(w, http.StatusBadRequest, "content_violation", "content flagged for: "+modResult.Reason)
		return
	}

	// Estimate max cost based on input + max_tokens output
	estimatedMaxCost := h.billing.EstimateCost(req.Model, inputText, req.MaxTokens)
	if estimatedMaxCost < 1 {
		estimatedMaxCost = 1
	}

	// Check wallet balance >= estimated max cost
	balanceSats, err := nwcClient.GetBalance(ctx, 10*time.Second)
	if err != nil {
		slog.Warn("failed to check wallet balance", "error", err)
		// Continue anyway - some wallets may not support get_balance
	} else {
		if balanceSats < estimatedMaxCost {
			slog.Info("wallet balance too low for estimated cost",
				"balance_sats", balanceSats,
				"estimated_max_cost_sats", estimatedMaxCost,
			)
			writeError(w, http.StatusPaymentRequired, "insufficient_balance",
				fmt.Sprintf("Insufficient balance: have %d sats, need %d sats for this request",
					balanceSats, estimatedMaxCost))
			return
		}
		slog.Debug("wallet balance OK", "balance_sats", balanceSats, "estimated_max_cost_sats", estimatedMaxCost)
	}

	slog.Info("processing request",
		"model", req.Model,
		"max_tokens", req.MaxTokens,
		"estimated_max_cost_sats", estimatedMaxCost,
		"stream", req.Stream,
	)

	// Get provider
	prov, err := h.providerRouter.GetProvider(req.Model)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_model", "model not supported")
		return
	}

	// Handle streaming requests
	if req.Stream {
		h.handleStreamingResponse(w, ctx, prov, &req, nwcClient, inputText)
		return
	}

	// Call AI provider
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

	// Calculate actual cost
	usage := billing.Usage{
		PromptTokens:     resp.Usage.PromptTokens,
		CompletionTokens: resp.Usage.CompletionTokens,
		Model:            req.Model,
	}
	actualCost, _ := h.billing.Calculate(usage)

	// Post-charge actual cost
	var chargeStatus string
	if actualCost.TotalSats > 0 {
		invoice, err := h.blinkClient.CreateInvoice(ctx, actualCost.TotalSats, fmt.Sprintf("Trandor: %s request", req.Model))
		if err != nil {
			slog.Error("failed to create invoice for post-charge", "error", err, "amount", actualCost.TotalSats)
			chargeStatus = "invoice_failed"
		} else {
			_, err = nwcClient.PayInvoice(ctx, invoice.PaymentRequest, 30*time.Second)
			if err != nil {
				slog.Warn("post-charge failed, accepting loss",
					"error", err,
					"amount_sats", actualCost.TotalSats,
					"wallet_pubkey", walletPubkey,
				)
				chargeStatus = "payment_failed"
			} else {
				slog.Info("post-charge successful", "amount_sats", actualCost.TotalSats)
				chargeStatus = "success"
			}
		}
	} else {
		chargeStatus = "zero_cost"
	}

	slog.Info("request completed",
		"model", req.Model,
		"prompt_tokens", resp.Usage.PromptTokens,
		"completion_tokens", resp.Usage.CompletionTokens,
		"cost_usd", fmt.Sprintf("$%.8f", actualCost.TotalUSD),
		"cost_sats", actualCost.TotalSats,
		"charge_status", chargeStatus,
	)

	// Return response with cost info
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Cost-Sats", fmt.Sprintf("%d", actualCost.TotalSats))
	w.Header().Set("X-Cost-USD", fmt.Sprintf("%.6f", actualCost.TotalUSD))
	w.Header().Set("X-Charge-Status", chargeStatus)
	json.NewEncoder(w).Encode(resp)
}

// handleStreamingResponse handles streaming chat completions with SSE format
// Proxies SSE chunks from upstream OpenAI provider
func (h *NWCHandler) handleStreamingResponse(
	w http.ResponseWriter,
	ctx context.Context,
	prov provider.Provider,
	req *provider.ChatRequest,
	nwcClient *nwc.Client,
	inputText string,
) {
	// Set SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Should-Retry", "false") // Prevent OpenAI SDK from auto-retrying

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming_not_supported", "streaming not supported by server")
		return
	}

	// Cast to OpenAI provider for streaming
	openaiProv, ok := prov.(*openai.Provider)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming_not_supported", "provider does not support streaming")
		return
	}

	// Ensure stream options include usage
	if req.StreamOptions == nil {
		req.StreamOptions = &provider.ChatStreamOptions{IncludeUsage: true}
	} else {
		req.StreamOptions.IncludeUsage = true
	}

	stream, err := openaiProv.ChatStream(ctx, req)
	if err != nil {
		if openaiErr, ok := err.(*openai.OpenAIError); ok {
			// Send error as SSE
			errData, _ := json.Marshal(map[string]interface{}{
				"error": map[string]string{
					"message": openaiErr.Message,
					"type":    openaiErr.Type,
					"code":    openaiErr.Code,
				},
			})
			fmt.Fprintf(w, "data: %s\n\n", errData)
			flusher.Flush()
			return
		}
		slog.Error("provider stream error", "error", err)
		errData, _ := json.Marshal(map[string]interface{}{
			"error": map[string]string{
				"message": "upstream provider error",
				"type":    "provider_error",
			},
		})
		fmt.Fprintf(w, "data: %s\n\n", errData)
		flusher.Flush()
		return
	}
	defer stream.Close()

	// Proxy SSE chunks from upstream
	var usage *provider.ChatUsage
	var receivedDone bool

	for {
		line, ok, err := stream.Next()
		if err != nil {
			slog.Error("stream read error", "error", err)
			break
		}
		if !ok {
			break
		}

		// Skip empty lines
		if line == "" {
			continue
		}

		// Forward the SSE line as-is (it already has "data: " prefix from OpenAI)
		if strings.HasPrefix(line, "data: ") {
			fmt.Fprintf(w, "%s\n\n", line)
			flusher.Flush()

			// Check for [DONE] marker
			if line == "data: [DONE]" {
				receivedDone = true
				continue
			}

			// Parse chunk to capture usage
			chunk, parseErr := openai.ParseStreamChunk(line)
			if parseErr == nil && chunk != nil && chunk.Usage != nil {
				usage = chunk.Usage
			}
		}
	}

	// Only send [DONE] if we didn't receive it from upstream
	if !receivedDone {
		fmt.Fprintf(w, "data: [DONE]\n\n")
		flusher.Flush()
	}

	// Post-charge actual cost after streaming completes
	go h.processStreamingPostCharge(ctx, nwcClient, usage, req.Model)
}

// processStreamingPostCharge handles post-charging after streaming completes
func (h *NWCHandler) processStreamingPostCharge(
	ctx context.Context,
	nwcClient *nwc.Client,
	usage *provider.ChatUsage,
	model string,
) {
	if usage == nil {
		slog.Warn("no usage data from streaming response, cannot calculate cost")
		return
	}

	usageCalc := billing.Usage{
		PromptTokens:     usage.PromptTokens,
		CompletionTokens: usage.CompletionTokens,
		Model:            model,
	}
	actualCost, _ := h.billing.Calculate(usageCalc)

	if actualCost.TotalSats <= 0 {
		slog.Info("streaming request completed, zero cost",
			"model", model,
			"prompt_tokens", usage.PromptTokens,
			"completion_tokens", usage.CompletionTokens,
		)
		return
	}

	// Create charge context with timeout
	chargeCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	invoice, err := h.blinkClient.CreateInvoice(chargeCtx, actualCost.TotalSats, fmt.Sprintf("Trandor: %s streaming", model))
	if err != nil {
		slog.Error("failed to create invoice for streaming post-charge", "error", err, "amount", actualCost.TotalSats)
		return
	}

	_, err = nwcClient.PayInvoice(chargeCtx, invoice.PaymentRequest, 30*time.Second)
	if err != nil {
		slog.Warn("streaming post-charge failed, accepting loss",
			"error", err,
			"amount_sats", actualCost.TotalSats,
			"wallet_pubkey", nwcClient.WalletPubkey(),
		)
		return
	}

	slog.Info("streaming post-charge successful",
		"model", model,
		"amount_sats", actualCost.TotalSats,
		"prompt_tokens", usage.PromptTokens,
		"completion_tokens", usage.CompletionTokens,
	)
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

	nwcURL, err := extractNWCFromAuth(r)
	if err != nil {
		sendStep("wallet_connect", "error", nil)
		sendError("unauthorized", err.Error())
		return
	}

	nwcClient, err := nwc.NewClient(nwcURL)
	if err != nil {
		sendStep("wallet_connect", "error", nil)
		sendError("invalid_credentials", "Invalid NWC connection: "+err.Error())
		return
	}
	defer nwcClient.Close()

	// Rate limit check
	walletPubkey := nwcClient.WalletPubkey()
	if err := h.rateLimiter.Acquire(walletPubkey); err != nil {
		sendStep("wallet_connect", "error", nil)
		sendError("rate_limited", "Too many concurrent requests")
		return
	}
	defer h.rateLimiter.Release(walletPubkey)

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
		inputText += msg.Content.String() + " "
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

	// Step 3: Balance check
	sendStep("balance_check", "pending", nil)

	estimatedMaxCost := h.billing.EstimateCost(req.Model, inputText, req.MaxTokens)
	if estimatedMaxCost < 1 {
		estimatedMaxCost = 1
	}

	balanceSats, err := nwcClient.GetBalance(ctx, 10*time.Second)
	if err != nil {
		slog.Warn("failed to check wallet balance", "error", err)
		sendStep("balance_check", "complete", map[string]interface{}{
			"warning":            "balance check unavailable",
			"estimated_max_sats": estimatedMaxCost,
		})
	} else {
		if balanceSats < estimatedMaxCost {
			sendStep("balance_check", "error", nil)
			sendError("insufficient_balance", fmt.Sprintf("Need %d sats, have %d", estimatedMaxCost, balanceSats))
			return
		}
		sendStep("balance_check", "complete", map[string]interface{}{
			"balance_sats":       balanceSats,
			"estimated_max_sats": estimatedMaxCost,
		})
	}

	// Step 4: AI generation (streaming)
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

	// Ensure stream options include usage
	if req.StreamOptions == nil {
		req.StreamOptions = &provider.ChatStreamOptions{IncludeUsage: true}
	} else {
		req.StreamOptions.IncludeUsage = true
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

	// Step 5: Cost calculation and post-charge
	sendStep("payment", "pending", nil)

	var actualCostSats int64 = 0
	var actualCostUSD float64 = 0
	var chargeStatus string

	if usage != nil {
		usageCalc := billing.Usage{
			PromptTokens:     usage.PromptTokens,
			CompletionTokens: usage.CompletionTokens,
			Model:            req.Model,
		}
		actualCost, _ := h.billing.Calculate(usageCalc)
		actualCostSats = actualCost.TotalSats
		actualCostUSD = actualCost.TotalUSD

		if actualCostSats > 0 {
			invoice, err := h.blinkClient.CreateInvoice(ctx, actualCostSats, fmt.Sprintf("Trandor: %s request", req.Model))
			if err != nil {
				slog.Error("failed to create invoice for post-charge", "error", err)
				chargeStatus = "invoice_failed"
			} else {
				_, err = nwcClient.PayInvoice(ctx, invoice.PaymentRequest, 30*time.Second)
				if err != nil {
					slog.Warn("post-charge failed, accepting loss", "error", err, "amount", actualCostSats)
					chargeStatus = "payment_failed"
				} else {
					chargeStatus = "success"
				}
			}
		} else {
			chargeStatus = "zero_cost"
		}
	} else {
		chargeStatus = "no_usage"
	}

	sendStep("payment", "complete", map[string]interface{}{
		"cost_sats":     actualCostSats,
		"cost_usd":      actualCostUSD,
		"charge_status": chargeStatus,
	})

	// Send final summary
	usageMap := map[string]interface{}{}
	if usage != nil {
		usageMap["prompt_tokens"] = usage.PromptTokens
		usageMap["completion_tokens"] = usage.CompletionTokens
	}

	sendSSE(w, flusher, "complete", map[string]interface{}{
		"content":       fullContent.String(),
		"cost_sats":     actualCostSats,
		"cost_usd":      actualCostUSD,
		"charge_status": chargeStatus,
		"usage":         usageMap,
	})

	slog.Info("streaming request completed",
		"model", req.Model,
		"cost_sats", actualCostSats,
		"charge_status", chargeStatus,
	)
}
