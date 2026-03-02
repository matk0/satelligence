package blink

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

type WebhookEvent struct {
	Type        string          `json:"type"`
	PaymentHash string          `json:"paymentHash"`
	AmountSats  int64           `json:"amount"`
	Status      string          `json:"status"`
	RawPayload  json.RawMessage `json:"-"`
}

type WebhookHandler struct {
	secret string
}

func NewWebhookHandler(secret string) *WebhookHandler {
	return &WebhookHandler{secret: secret}
}

func (h *WebhookHandler) ValidateSignature(body []byte, signature string) bool {
	if h.secret == "" {
		return true // No secret configured, skip validation
	}

	mac := hmac.New(sha256.New, []byte(h.secret))
	mac.Write(body)
	expectedSignature := hex.EncodeToString(mac.Sum(nil))

	return hmac.Equal([]byte(signature), []byte(expectedSignature))
}

func (h *WebhookHandler) ParseEvent(r *http.Request) (*WebhookEvent, error) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read body: %w", err)
	}

	signature := r.Header.Get("X-Blink-Signature")
	if !h.ValidateSignature(body, signature) {
		return nil, fmt.Errorf("invalid webhook signature")
	}

	var event WebhookEvent
	if err := json.Unmarshal(body, &event); err != nil {
		return nil, fmt.Errorf("failed to parse event: %w", err)
	}

	event.RawPayload = body
	return &event, nil
}
