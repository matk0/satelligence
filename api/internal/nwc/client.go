package nwc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
)

var (
	ErrPaymentFailed  = errors.New("payment failed")
	ErrPaymentTimeout = errors.New("payment timeout")
	ErrInvalidNWCURL  = errors.New("invalid NWC connection URL")
)

// ConnectionInfo holds parsed NWC connection details
type ConnectionInfo struct {
	WalletPubkey string
	RelayURL     string
	Secret       string
}

// Client handles NWC communication
type Client struct {
	connInfo   *ConnectionInfo
	secretKey  string
	pubKey     string
	relay      *nostr.Relay
	mu         sync.Mutex
	connected  bool
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

	return &Client{
		connInfo:  connInfo,
		secretKey: connInfo.Secret,
		pubKey:    pubKey,
	}, nil
}

// Connect establishes connection to the relay
func (c *Client) Connect(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.connected && c.relay != nil {
		return nil
	}

	relay, err := nostr.RelayConnect(ctx, c.connInfo.RelayURL)
	if err != nil {
		return fmt.Errorf("failed to connect to relay: %w", err)
	}

	c.relay = relay
	c.connected = true
	return nil
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

// sendRequest sends an NWC request and returns the response
func (c *Client) sendRequest(ctx context.Context, method string, params interface{}, timeout time.Duration) (*NWCResponse, error) {
	if err := c.Connect(ctx); err != nil {
		return nil, err
	}

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

	sub, err := c.relay.Subscribe(timeoutCtx, filters)
	if err != nil {
		return nil, fmt.Errorf("failed to subscribe: %w", err)
	}
	defer sub.Close()

	// Publish the request
	if err := c.relay.Publish(timeoutCtx, event); err != nil {
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
