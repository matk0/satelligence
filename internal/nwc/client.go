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

// PayInvoiceRequest represents a pay_invoice NWC request
type PayInvoiceRequest struct {
	Method string `json:"method"`
	Params struct {
		Invoice string `json:"invoice"`
	} `json:"params"`
}

// PayInvoiceResponse represents a pay_invoice NWC response
type PayInvoiceResponse struct {
	ResultType string `json:"result_type"`
	Result     *struct {
		Preimage string `json:"preimage"`
	} `json:"result,omitempty"`
	Error *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// PayInvoice sends a payment request via NWC and waits for confirmation
func (c *Client) PayInvoice(ctx context.Context, invoice string, timeout time.Duration) (preimage string, err error) {
	if err := c.Connect(ctx); err != nil {
		return "", err
	}

	// Create the request payload
	reqPayload := PayInvoiceRequest{
		Method: "pay_invoice",
	}
	reqPayload.Params.Invoice = invoice

	payloadBytes, err := json.Marshal(reqPayload)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	// Compute shared secret for NIP-04 encryption
	sharedSecret, err := nip04.ComputeSharedSecret(c.connInfo.WalletPubkey, c.secretKey)
	if err != nil {
		return "", fmt.Errorf("failed to compute shared secret: %w", err)
	}

	// Encrypt the payload using NIP-04
	encryptedContent, err := nip04.Encrypt(string(payloadBytes), sharedSecret)
	if err != nil {
		return "", fmt.Errorf("failed to encrypt request: %w", err)
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
		return "", fmt.Errorf("failed to sign event: %w", err)
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
		return "", fmt.Errorf("failed to subscribe: %w", err)
	}
	defer sub.Close()

	// Publish the request
	if err := c.relay.Publish(timeoutCtx, event); err != nil {
		return "", fmt.Errorf("failed to publish request: %w", err)
	}

	// Wait for response
	select {
	case evt := <-sub.Events:
		// Compute shared secret for decryption
		sharedSecret, err := nip04.ComputeSharedSecret(c.connInfo.WalletPubkey, c.secretKey)
		if err != nil {
			return "", fmt.Errorf("failed to compute shared secret: %w", err)
		}

		// Decrypt the response
		decrypted, err := nip04.Decrypt(evt.Content, sharedSecret)
		if err != nil {
			return "", fmt.Errorf("failed to decrypt response: %w", err)
		}

		var response PayInvoiceResponse
		if err := json.Unmarshal([]byte(decrypted), &response); err != nil {
			return "", fmt.Errorf("failed to parse response: %w", err)
		}

		if response.Error != nil {
			return "", fmt.Errorf("%w: %s - %s", ErrPaymentFailed, response.Error.Code, response.Error.Message)
		}

		if response.Result == nil || response.Result.Preimage == "" {
			return "", ErrPaymentFailed
		}

		return response.Result.Preimage, nil

	case <-timeoutCtx.Done():
		return "", ErrPaymentTimeout
	}
}
