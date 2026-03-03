package blink

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"
)

const (
	BlinkAPIURL = "https://api.blink.sv/graphql"
)

type Client struct {
	apiKey      string
	btcWalletID string
	usdWalletID string
	httpClient  *http.Client
}

func NewClient(apiKey string) *Client {
	c := &Client{
		apiKey: apiKey,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}

	// Fetch wallet IDs if API key is provided
	if apiKey != "" {
		if err := c.fetchWalletIDs(context.Background()); err != nil {
			slog.Warn("failed to fetch Blink wallet IDs", "error", err)
		} else {
			slog.Info("fetched Blink wallet IDs", "btc_wallet_id", c.btcWalletID, "usd_wallet_id", c.usdWalletID)
		}
	}

	return c
}

type walletResponse struct {
	Me struct {
		DefaultAccount struct {
			Wallets []struct {
				ID             string `json:"id"`
				WalletCurrency string `json:"walletCurrency"`
			} `json:"wallets"`
		} `json:"defaultAccount"`
	} `json:"me"`
}

func (c *Client) fetchWalletIDs(ctx context.Context) error {
	query := `
		query Me {
			me {
				defaultAccount {
					wallets {
						id
						walletCurrency
					}
				}
			}
		}
	`

	var result walletResponse
	if err := c.execute(ctx, query, nil, &result); err != nil {
		return err
	}

	// Find BTC and USD wallets
	for _, wallet := range result.Me.DefaultAccount.Wallets {
		switch wallet.WalletCurrency {
		case "BTC":
			c.btcWalletID = wallet.ID
		case "USD":
			c.usdWalletID = wallet.ID
		}
	}

	if c.btcWalletID == "" {
		return fmt.Errorf("no BTC wallet found")
	}

	return nil
}

func (c *Client) GetWalletID() string {
	return c.btcWalletID
}

func (c *Client) GetBTCWalletID() string {
	return c.btcWalletID
}

func (c *Client) GetUSDWalletID() string {
	return c.usdWalletID
}

// WalletBalances represents the balances of BTC and USD wallets
type WalletBalances struct {
	BTCSats  int64
	USDCents int64
}

type walletBalancesResponse struct {
	Me struct {
		DefaultAccount struct {
			Wallets []struct {
				ID             string `json:"id"`
				WalletCurrency string `json:"walletCurrency"`
				Balance        int64  `json:"balance"`
			} `json:"wallets"`
		} `json:"defaultAccount"`
	} `json:"me"`
}

// GetWalletBalances returns the current balances of BTC and USD wallets
func (c *Client) GetWalletBalances(ctx context.Context) (*WalletBalances, error) {
	if c.apiKey == "" {
		// Development mode
		return &WalletBalances{
			BTCSats:  10000,
			USDCents: 5000,
		}, nil
	}

	query := `
		query Me {
			me {
				defaultAccount {
					wallets {
						id
						walletCurrency
						balance
					}
				}
			}
		}
	`

	var result walletBalancesResponse
	if err := c.execute(ctx, query, nil, &result); err != nil {
		return nil, err
	}

	balances := &WalletBalances{}
	for _, wallet := range result.Me.DefaultAccount.Wallets {
		switch wallet.WalletCurrency {
		case "BTC":
			balances.BTCSats = wallet.Balance
		case "USD":
			balances.USDCents = wallet.Balance
		}
	}

	return balances, nil
}

// TransferResult represents the result of an intra-ledger transfer
type TransferResult struct {
	Status        string
	TransactionID string
}

type intraLedgerPaymentSendResponse struct {
	IntraLedgerPaymentSend struct {
		Status string `json:"status"`
		Errors []struct {
			Message string `json:"message"`
			Code    string `json:"code"`
		} `json:"errors"`
		Transaction struct {
			ID string `json:"id"`
		} `json:"transaction"`
	} `json:"intraLedgerPaymentSend"`
}

// TransferBTCToUSD transfers sats from BTC wallet to USD wallet (Stablesats)
// This is an intra-ledger transfer with ~0.2% spread and no fees
func (c *Client) TransferBTCToUSD(ctx context.Context, amountSats int64) (*TransferResult, error) {
	if c.apiKey == "" {
		// Development mode
		return &TransferResult{
			Status:        "SUCCESS",
			TransactionID: "dev_transfer_id",
		}, nil
	}

	if c.usdWalletID == "" {
		return nil, fmt.Errorf("USD wallet not configured")
	}

	query := `
		mutation IntraLedgerPaymentSend($input: IntraLedgerPaymentSendInput!) {
			intraLedgerPaymentSend(input: $input) {
				status
				errors {
					message
					code
				}
				transaction {
					id
				}
			}
		}
	`

	variables := map[string]interface{}{
		"input": map[string]interface{}{
			"walletId":          c.btcWalletID,
			"recipientWalletId": c.usdWalletID,
			"amount":            amountSats,
		},
	}

	var result intraLedgerPaymentSendResponse
	if err := c.execute(ctx, query, variables, &result); err != nil {
		return nil, err
	}

	if len(result.IntraLedgerPaymentSend.Errors) > 0 {
		return nil, fmt.Errorf("transfer failed: %s", result.IntraLedgerPaymentSend.Errors[0].Message)
	}

	return &TransferResult{
		Status:        result.IntraLedgerPaymentSend.Status,
		TransactionID: result.IntraLedgerPaymentSend.Transaction.ID,
	}, nil
}

// PaymentResult represents the result of a Lightning payment
type PaymentResult struct {
	Status   string
	Preimage string
}

type lnInvoicePaymentSendResponse struct {
	LnInvoicePaymentSend struct {
		Status string `json:"status"`
		Errors []struct {
			Message string `json:"message"`
			Code    string `json:"code"`
		} `json:"errors"`
	} `json:"lnInvoicePaymentSend"`
}

// PayInvoice pays a Lightning invoice from the Blink wallet
func (c *Client) PayInvoice(ctx context.Context, paymentRequest string) (*PaymentResult, error) {
	if c.apiKey == "" {
		// Development mode
		return &PaymentResult{
			Status:   "SUCCESS",
			Preimage: "dev_preimage",
		}, nil
	}

	query := `
		mutation LnInvoicePaymentSend($input: LnInvoicePaymentInput!) {
			lnInvoicePaymentSend(input: $input) {
				status
				errors {
					message
					code
				}
			}
		}
	`

	variables := map[string]interface{}{
		"input": map[string]interface{}{
			"paymentRequest": paymentRequest,
			"walletId":       c.btcWalletID,
		},
	}

	var result lnInvoicePaymentSendResponse
	if err := c.execute(ctx, query, variables, &result); err != nil {
		return nil, err
	}

	if len(result.LnInvoicePaymentSend.Errors) > 0 {
		return nil, fmt.Errorf("payment failed: %s", result.LnInvoicePaymentSend.Errors[0].Message)
	}

	return &PaymentResult{
		Status: result.LnInvoicePaymentSend.Status,
	}, nil
}

type graphQLRequest struct {
	Query     string                 `json:"query"`
	Variables map[string]interface{} `json:"variables,omitempty"`
}

type graphQLResponse struct {
	Data   json.RawMessage `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors,omitempty"`
}

func (c *Client) execute(ctx context.Context, query string, variables map[string]interface{}, result interface{}) error {
	reqBody := graphQLRequest{
		Query:     query,
		Variables: variables,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", BlinkAPIURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-KEY", c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	var gqlResp graphQLResponse
	if err := json.Unmarshal(respBody, &gqlResp); err != nil {
		return fmt.Errorf("failed to unmarshal response: %w", err)
	}

	if len(gqlResp.Errors) > 0 {
		return fmt.Errorf("graphql error: %s", gqlResp.Errors[0].Message)
	}

	if result != nil {
		if err := json.Unmarshal(gqlResp.Data, result); err != nil {
			return fmt.Errorf("failed to unmarshal data: %w", err)
		}
	}

	return nil
}
