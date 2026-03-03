package lnbits

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"crypto/rand"
)

// Client interacts with LNbits API for hosted wallet management
type Client struct {
	baseURL    string
	adminKey   string
	httpClient *http.Client
}

// NewClient creates a new LNbits client
func NewClient(baseURL, adminKey string) *Client {
	return &Client{
		baseURL:  baseURL,
		adminKey: adminKey,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// IsConfigured returns true if LNbits is configured
func (c *Client) IsConfigured() bool {
	return c.baseURL != "" && c.adminKey != ""
}

// User represents an LNbits user
type User struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Wallet represents an LNbits wallet
type Wallet struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	UserID    string `json:"user"`
	AdminKey  string `json:"adminkey"`
	InvoiceKey string `json:"inkey"`
	Balance   int64  `json:"balance_msat"`
}

// NWCConnection represents an NWC connection
type NWCConnection struct {
	Pubkey      string `json:"pubkey"`
	Description string `json:"description"`
	PairingURL  string `json:"pairing_url"`
}

// Invoice represents a Lightning invoice
type Invoice struct {
	PaymentHash    string `json:"payment_hash"`
	PaymentRequest string `json:"payment_request"`
	CheckingID     string `json:"checking_id"`
}

// CreateUserRequest is the request body for creating a user
type CreateUserRequest struct {
	UserName   string `json:"user_name"`
	WalletName string `json:"wallet_name"`
}

// CreateUserResponse is the response from creating a user
type CreateUserResponse struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Wallets []Wallet `json:"wallets"`
}

// CreateUser creates a new LNbits user with an initial wallet
func (c *Client) CreateUser(ctx context.Context, userName, walletName string) (*CreateUserResponse, error) {
	reqBody := CreateUserRequest{
		UserName:   userName,
		WalletName: walletName,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/usermanager/api/v1/users", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Api-Key", c.adminKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("LNbits API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	var result CreateUserResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	return &result, nil
}

// CreateWallet creates a new wallet for an existing user
func (c *Client) CreateWallet(ctx context.Context, userID, walletName string) (*Wallet, error) {
	reqBody := map[string]string{
		"user_id":     userID,
		"wallet_name": walletName,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/usermanager/api/v1/wallets", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Api-Key", c.adminKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("LNbits API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	var result Wallet
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	return &result, nil
}

// GetWalletBalance gets the balance for a wallet using its invoice key
func (c *Client) GetWalletBalance(ctx context.Context, invoiceKey string) (int64, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+"/api/v1/wallet", nil)
	if err != nil {
		return 0, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("X-Api-Key", invoiceKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("LNbits API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Balance int64 `json:"balance"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return 0, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	// LNbits returns balance in millisats, convert to sats
	return result.Balance / 1000, nil
}

// CreateInvoice creates a Lightning invoice for depositing funds
func (c *Client) CreateInvoice(ctx context.Context, invoiceKey string, amountSats int64, memo string) (*Invoice, error) {
	reqBody := map[string]interface{}{
		"out":    false,
		"amount": amountSats,
		"memo":   memo,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/api/v1/payments", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Api-Key", invoiceKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("LNbits API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	var result Invoice
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	return &result, nil
}

// generateRandomHex generates a random hex string of the given byte length
func generateRandomHex(byteLen int) (string, error) {
	b := make([]byte, byteLen)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// CreateNWCConnection creates an NWC connection for a wallet
// Returns the pairing URL (nostr+walletconnect://...)
func (c *Client) CreateNWCConnection(ctx context.Context, walletAdminKey, description string) (*NWCConnection, error) {
	// Generate a random 32-byte pubkey for the NWC connection
	pubkey, err := generateRandomHex(32)
	if err != nil {
		return nil, fmt.Errorf("failed to generate pubkey: %w", err)
	}

	// Create NWC connection via PUT /nwcprovider/api/v1/nwc/{pubkey}
	reqBody := map[string]interface{}{
		"description": description,
		"permissions": []string{"pay_invoice", "make_invoice", "lookup_invoice", "get_balance", "list_transactions"},
		"budgets":     []interface{}{}, // No budget limits
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "PUT", c.baseURL+"/nwcprovider/api/v1/nwc/"+pubkey, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Api-Key", walletAdminKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("LNbits NWC API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	// Now get the pairing URL
	pairingURL, err := c.getNWCPairingURL(ctx, walletAdminKey, pubkey)
	if err != nil {
		return nil, fmt.Errorf("failed to get pairing URL: %w", err)
	}

	return &NWCConnection{
		Pubkey:      pubkey,
		Description: description,
		PairingURL:  pairingURL,
	}, nil
}

// getNWCPairingURL gets the pairing URL for an NWC connection
func (c *Client) getNWCPairingURL(ctx context.Context, walletAdminKey, pubkey string) (string, error) {
	// The pairing endpoint uses secret, which is derived from pubkey
	// GET /nwcprovider/api/v1/pairing/{secret}
	// For simplicity, we'll get the NWC details which should include the pairing info
	req, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+"/nwcprovider/api/v1/nwc/"+pubkey, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("X-Api-Key", walletAdminKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("LNbits NWC API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Pubkey     string `json:"pubkey"`
		Secret     string `json:"secret"`
		PairingURI string `json:"pairing_uri"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("failed to unmarshal response: %w", err)
	}

	// If pairing_uri is directly in the response, use it
	if result.PairingURI != "" {
		return result.PairingURI, nil
	}

	// Otherwise, fetch from pairing endpoint using secret
	if result.Secret != "" {
		return c.fetchPairingURL(ctx, result.Secret)
	}

	return "", fmt.Errorf("could not determine pairing URL")
}

// fetchPairingURL fetches the pairing URL using the secret
func (c *Client) fetchPairingURL(ctx context.Context, secret string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+"/nwcprovider/api/v1/pairing/"+secret, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("LNbits pairing API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		PairingURI string `json:"pairing_uri"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		// The response might be just the URI string
		return string(respBody), nil
	}

	return result.PairingURI, nil
}

// GetUserWallets gets all wallets for a user
func (c *Client) GetUserWallets(ctx context.Context, userID string) ([]Wallet, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+"/usermanager/api/v1/wallets/"+userID, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("X-Api-Key", c.adminKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("LNbits API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	var result []Wallet
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	return result, nil
}

// GetWallet gets a specific wallet by admin key
func (c *Client) GetWallet(ctx context.Context, adminKey string) (*Wallet, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+"/api/v1/wallet", nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("X-Api-Key", adminKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("LNbits API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		ID      string `json:"id"`
		Name    string `json:"name"`
		Balance int64  `json:"balance"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	return &Wallet{
		ID:       result.ID,
		Name:     result.Name,
		Balance:  result.Balance,
		AdminKey: adminKey,
	}, nil
}
