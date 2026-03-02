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
	apiKey     string
	walletID   string
	httpClient *http.Client
}

func NewClient(apiKey string) *Client {
	c := &Client{
		apiKey: apiKey,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}

	// Fetch wallet ID if API key is provided
	if apiKey != "" {
		if err := c.fetchWalletID(context.Background()); err != nil {
			slog.Warn("failed to fetch Blink wallet ID", "error", err)
		} else {
			slog.Info("fetched Blink wallet ID", "wallet_id", c.walletID)
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

func (c *Client) fetchWalletID(ctx context.Context) error {
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

	// Find BTC wallet
	for _, wallet := range result.Me.DefaultAccount.Wallets {
		if wallet.WalletCurrency == "BTC" {
			c.walletID = wallet.ID
			return nil
		}
	}

	return fmt.Errorf("no BTC wallet found")
}

func (c *Client) GetWalletID() string {
	return c.walletID
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
