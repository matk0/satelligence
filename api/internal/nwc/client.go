package nwc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/nbd-wtf/go-nostr"
	"github.com/nbd-wtf/go-nostr/nip04"
)

const (
	KindNWCRequest  = 23194
	KindNWCResponse = 23195

	// Retry configuration
	maxRetries     = 3
	initialBackoff = 500 * time.Millisecond
	maxBackoff     = 5 * time.Second
)

// BackupRelays are fallback relays to try if the primary relay fails.
// For hosted wallets, relay.trandor.com is our own relay where LNbits listens.
// For BYONWC (Alby, etc.), the primary relay in their connection string is used first.
var BackupRelays = []string{
	"wss://relay.trandor.com", // Our own relay (for hosted wallets)
	"wss://relay.damus.io",    // Popular public relay
	"wss://nos.lol",
	"wss://relay.nostr.band",
}

var (
	ErrPaymentFailed  = errors.New("payment failed")
	ErrPaymentTimeout = errors.New("payment timeout")
	ErrInvalidNWCURL  = errors.New("invalid NWC connection URL")
	ErrRelayFailed    = errors.New("relay connection failed")
)

// IsInfrastructureError returns true if the error is due to infrastructure issues
// (relay failures, timeouts, etc.) rather than wallet-level payment failures.
// Infrastructure errors should NOT cause wallet blacklisting.
func IsInfrastructureError(err error) bool {
	if err == nil {
		return false
	}
	// Timeout is infrastructure - the wallet never got to respond
	if errors.Is(err, ErrPaymentTimeout) {
		return true
	}
	// Relay failures are infrastructure
	if errors.Is(err, ErrRelayFailed) {
		return true
	}
	// Check for wrapped relay/connection errors by message
	errStr := err.Error()
	if strings.Contains(errStr, "failed to connect to relay") ||
		strings.Contains(errStr, "failed to subscribe") ||
		strings.Contains(errStr, "failed to publish") {
		return true
	}
	return false
}

// IsPaymentError returns true if the error indicates the wallet explicitly refused
// or failed to complete the payment. These errors should trigger blacklisting.
func IsPaymentError(err error) bool {
	if err == nil {
		return false
	}
	// Check if it's explicitly a payment failure from the wallet
	return errors.Is(err, ErrPaymentFailed)
}

// ConnectionInfo holds parsed NWC connection details
type ConnectionInfo struct {
	WalletPubkey string
	RelayURL     string
	Secret       string
}

// Client handles NWC communication
type Client struct {
	connInfo      *ConnectionInfo
	secretKey     string
	pubKey        string
	relay         *nostr.Relay
	currentRelay  string // URL of currently connected relay
	mu            sync.Mutex
	connected     bool
	relaysToTry   []string // Primary relay + backups
}

// WalletPubkey returns the wallet's public key (used for credit tracking)
func (c *Client) WalletPubkey() string {
	return c.connInfo.WalletPubkey
}

// ParseConnectionURL parses a nostr+walletconnect:// URL
func ParseConnectionURL(nwcURL string) (*ConnectionInfo, error) {
	// Format: nostr+walletconnect://pubkey?relay=wss://...&secret=...
	if !strings.HasPrefix(nwcURL, "nostr+walletconnect://") {
		return nil, ErrInvalidNWCURL
	}

	// Remove the scheme
	rest := strings.TrimPrefix(nwcURL, "nostr+walletconnect://")

	// Split pubkey and query string
	parts := strings.SplitN(rest, "?", 2)
	if len(parts) != 2 {
		return nil, ErrInvalidNWCURL
	}

	walletPubkey := parts[0]
	queryString := parts[1]

	// Parse query parameters
	values, err := url.ParseQuery(queryString)
	if err != nil {
		return nil, fmt.Errorf("failed to parse query: %w", err)
	}

	relayURL := values.Get("relay")
	secret := values.Get("secret")

	if walletPubkey == "" || relayURL == "" || secret == "" {
		return nil, ErrInvalidNWCURL
	}

	return &ConnectionInfo{
		WalletPubkey: walletPubkey,
		RelayURL:     relayURL,
		Secret:       secret,
	}, nil
}

// NewClient creates a new NWC client from a connection URL
func NewClient(nwcURL string) (*Client, error) {
	connInfo, err := ParseConnectionURL(nwcURL)
	if err != nil {
		return nil, err
	}

	// The secret from the URL is the private key for signing requests
	pubKey, err := nostr.GetPublicKey(connInfo.Secret)
	if err != nil {
		return nil, fmt.Errorf("failed to derive public key: %w", err)
	}

	// Build list of relays to try: primary first, then backups
	relays := []string{connInfo.RelayURL}
	for _, backup := range BackupRelays {
		if backup != connInfo.RelayURL {
			relays = append(relays, backup)
		}
	}

	return &Client{
		connInfo:    connInfo,
		secretKey:   connInfo.Secret,
		pubKey:      pubKey,
		relaysToTry: relays,
	}, nil
}

// Connect establishes connection to a relay, trying multiple relays if needed
func (c *Client) Connect(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.connected && c.relay != nil {
		return nil
	}

	var lastErr error
	for _, relayURL := range c.relaysToTry {
		relay, err := c.connectToRelayWithRetry(ctx, relayURL)
		if err != nil {
			slog.Debug("failed to connect to relay, trying next",
				"relay", relayURL,
				"error", err)
			lastErr = err
			continue
		}

		c.relay = relay
		c.currentRelay = relayURL
		c.connected = true
		if relayURL != c.connInfo.RelayURL {
			slog.Info("connected to backup relay",
				"primary", c.connInfo.RelayURL,
				"backup", relayURL)
		}
		return nil
	}

	return fmt.Errorf("%w: all relays failed, last error: %v", ErrRelayFailed, lastErr)
}

// connectToRelayWithRetry attempts to connect to a single relay with exponential backoff
func (c *Client) connectToRelayWithRetry(ctx context.Context, relayURL string) (*nostr.Relay, error) {
	var lastErr error
	backoff := initialBackoff

	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			slog.Debug("retrying relay connection",
				"relay", relayURL,
				"attempt", attempt+1,
				"backoff", backoff)

			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff):
			}

			// Exponential backoff with cap
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
		}

		relay, err := nostr.RelayConnect(ctx, relayURL)
		if err == nil {
			return relay, nil
		}
		lastErr = err
	}

	return nil, lastErr
}

// reconnect forces a reconnection, useful after relay failures
func (c *Client) reconnect(ctx context.Context) error {
	c.mu.Lock()
	if c.relay != nil {
		c.relay.Close()
		c.relay = nil
	}
	c.connected = false
	c.mu.Unlock()

	return c.Connect(ctx)
}

// Close closes the relay connection
func (c *Client) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.relay != nil {
		c.relay.Close()
		c.relay = nil
		c.connected = false
	}
}

// NWCRequest represents a generic NWC request
type NWCRequest struct {
	Method string      `json:"method"`
	Params interface{} `json:"params"`
}

// PayInvoiceParams for pay_invoice method
type PayInvoiceParams struct {
	Invoice string `json:"invoice"`
}

// MakeInvoiceParams for make_invoice method
type MakeInvoiceParams struct {
	Amount      int64  `json:"amount"` // in millisats
	Description string `json:"description,omitempty"`
}

// NWCResponse represents a generic NWC response
type NWCResponse struct {
	ResultType string          `json:"result_type"`
	Result     json.RawMessage `json:"result,omitempty"`
	Error      *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// PayInvoiceResult for pay_invoice response
type PayInvoiceResult struct {
	Preimage string `json:"preimage"`
}

// MakeInvoiceResult for make_invoice response
type MakeInvoiceResult struct {
	Invoice     string `json:"invoice"`
	PaymentHash string `json:"payment_hash"`
}

// GetBalanceResult for get_balance response
type GetBalanceResult struct {
	Balance int64 `json:"balance"` // in millisats
}

// sendRequest sends an NWC request and returns the response, with retry logic
func (c *Client) sendRequest(ctx context.Context, method string, params interface{}, timeout time.Duration) (*NWCResponse, error) {
	var lastErr error
	backoff := initialBackoff

	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			slog.Debug("retrying NWC request",
				"method", method,
				"attempt", attempt+1,
				"backoff", backoff)

			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff):
			}

			// Exponential backoff
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}

			// Force reconnect on retry (try different relay)
			if err := c.reconnect(ctx); err != nil {
				lastErr = err
				continue
			}
		} else {
			// First attempt - just connect
			if err := c.Connect(ctx); err != nil {
				lastErr = err
				continue
			}
		}

		resp, err := c.sendRequestOnce(ctx, method, params, timeout)
		if err == nil {
			return resp, nil
		}

		// Only retry on infrastructure errors, not payment errors
		if !IsInfrastructureError(err) {
			return nil, err
		}

		lastErr = err
		slog.Warn("NWC request failed with infrastructure error, will retry",
			"method", method,
			"attempt", attempt+1,
			"error", err)
	}

	return nil, fmt.Errorf("all retry attempts failed: %w", lastErr)
}

// sendRequestOnce sends a single NWC request without retry
func (c *Client) sendRequestOnce(ctx context.Context, method string, params interface{}, timeout time.Duration) (*NWCResponse, error) {
	// Create the request payload
	reqPayload := NWCRequest{
		Method: method,
		Params: params,
	}

	payloadBytes, err := json.Marshal(reqPayload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Compute shared secret for NIP-04 encryption
	sharedSecret, err := nip04.ComputeSharedSecret(c.connInfo.WalletPubkey, c.secretKey)
	if err != nil {
		return nil, fmt.Errorf("failed to compute shared secret: %w", err)
	}

	// Encrypt the payload using NIP-04
	encryptedContent, err := nip04.Encrypt(string(payloadBytes), sharedSecret)
	if err != nil {
		return nil, fmt.Errorf("failed to encrypt request: %w", err)
	}

	// Create the Nostr event
	event := nostr.Event{
		Kind:      KindNWCRequest,
		Content:   encryptedContent,
		CreatedAt: nostr.Timestamp(time.Now().Unix()),
		Tags: nostr.Tags{
			{"p", c.connInfo.WalletPubkey},
		},
		PubKey: c.pubKey,
	}

	// Sign the event
	if err := event.Sign(c.secretKey); err != nil {
		return nil, fmt.Errorf("failed to sign event: %w", err)
	}

	// Subscribe to responses before publishing
	timeoutCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	filters := []nostr.Filter{{
		Kinds:   []int{KindNWCResponse},
		Authors: []string{c.connInfo.WalletPubkey},
		Tags:    nostr.TagMap{"e": []string{event.ID}},
	}}

	c.mu.Lock()
	relay := c.relay
	c.mu.Unlock()

	if relay == nil {
		return nil, fmt.Errorf("%w: no relay connection", ErrRelayFailed)
	}

	sub, err := relay.Subscribe(timeoutCtx, filters)
	if err != nil {
		return nil, fmt.Errorf("failed to subscribe: %w", err)
	}
	defer sub.Close()

	// Publish the request
	if err := relay.Publish(timeoutCtx, event); err != nil {
		return nil, fmt.Errorf("failed to publish request: %w", err)
	}

	// Wait for response
	select {
	case evt := <-sub.Events:
		// Decrypt the response
		decrypted, err := nip04.Decrypt(evt.Content, sharedSecret)
		if err != nil {
			return nil, fmt.Errorf("failed to decrypt response: %w", err)
		}

		var response NWCResponse
		if err := json.Unmarshal([]byte(decrypted), &response); err != nil {
			return nil, fmt.Errorf("failed to parse response: %w", err)
		}

		return &response, nil

	case <-timeoutCtx.Done():
		return nil, ErrPaymentTimeout
	}
}

// PayInvoice sends a payment request via NWC and waits for confirmation
func (c *Client) PayInvoice(ctx context.Context, invoice string, timeout time.Duration) (preimage string, err error) {
	resp, err := c.sendRequest(ctx, "pay_invoice", PayInvoiceParams{Invoice: invoice}, timeout)
	if err != nil {
		return "", err
	}

	if resp.Error != nil {
		return "", fmt.Errorf("%w: %s - %s", ErrPaymentFailed, resp.Error.Code, resp.Error.Message)
	}

	var result PayInvoiceResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return "", fmt.Errorf("failed to parse result: %w", err)
	}

	if result.Preimage == "" {
		return "", ErrPaymentFailed
	}

	return result.Preimage, nil
}

// MakeInvoice requests the user's wallet to create an invoice (for refunds)
// amountSats is in satoshis (will be converted to millisats)
func (c *Client) MakeInvoice(ctx context.Context, amountSats int64, description string, timeout time.Duration) (invoice string, err error) {
	// NWC uses millisats
	amountMsats := amountSats * 1000

	resp, err := c.sendRequest(ctx, "make_invoice", MakeInvoiceParams{
		Amount:      amountMsats,
		Description: description,
	}, timeout)
	if err != nil {
		return "", err
	}

	if resp.Error != nil {
		return "", fmt.Errorf("make_invoice failed: %s - %s", resp.Error.Code, resp.Error.Message)
	}

	var result MakeInvoiceResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return "", fmt.Errorf("failed to parse result: %w", err)
	}

	if result.Invoice == "" {
		return "", errors.New("no invoice in response")
	}

	return result.Invoice, nil
}

// GetBalance returns the wallet balance in satoshis
func (c *Client) GetBalance(ctx context.Context, timeout time.Duration) (balanceSats int64, err error) {
	resp, err := c.sendRequest(ctx, "get_balance", struct{}{}, timeout)
	if err != nil {
		return 0, err
	}

	if resp.Error != nil {
		return 0, fmt.Errorf("get_balance failed: %s - %s", resp.Error.Code, resp.Error.Message)
	}

	var result GetBalanceResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return 0, fmt.Errorf("failed to parse result: %w", err)
	}

	// Convert millisats to sats
	return result.Balance / 1000, nil
}
