package blink

import (
	"context"
	"fmt"
)

type Invoice struct {
	PaymentRequest string
	PaymentHash    string
}

type createInvoiceResponse struct {
	LnInvoiceCreate struct {
		Invoice struct {
			PaymentRequest string `json:"paymentRequest"`
			PaymentHash    string `json:"paymentHash"`
		} `json:"invoice"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	} `json:"lnInvoiceCreate"`
}

func (c *Client) CreateInvoice(ctx context.Context, amountSats int64, memo string) (*Invoice, error) {
	// Development mode: return mock invoice when no API key
	if c.apiKey == "" {
		return &Invoice{
			PaymentRequest: fmt.Sprintf("lnbc%dn1development_mode_invoice_%d", amountSats, amountSats),
			PaymentHash:    fmt.Sprintf("devhash_%d_%d", amountSats, ctx.Value("session_id")),
		}, nil
	}

	query := `
		mutation LnInvoiceCreate($input: LnInvoiceCreateInput!) {
			lnInvoiceCreate(input: $input) {
				invoice {
					paymentRequest
					paymentHash
				}
				errors {
					message
				}
			}
		}
	`

	variables := map[string]interface{}{
		"input": map[string]interface{}{
			"amount":   amountSats,
			"memo":     memo,
			"walletId": "BTC", // Use BTC wallet
		},
	}

	var result createInvoiceResponse
	if err := c.execute(ctx, query, variables, &result); err != nil {
		return nil, err
	}

	if len(result.LnInvoiceCreate.Errors) > 0 {
		return nil, fmt.Errorf("invoice creation error: %s", result.LnInvoiceCreate.Errors[0].Message)
	}

	return &Invoice{
		PaymentRequest: result.LnInvoiceCreate.Invoice.PaymentRequest,
		PaymentHash:    result.LnInvoiceCreate.Invoice.PaymentHash,
	}, nil
}

type InvoiceStatus string

const (
	InvoiceStatusPending InvoiceStatus = "PENDING"
	InvoiceStatusPaid    InvoiceStatus = "PAID"
	InvoiceStatusExpired InvoiceStatus = "EXPIRED"
)

type invoiceStatusResponse struct {
	LnInvoicePaymentStatus struct {
		Status InvoiceStatus `json:"status"`
	} `json:"lnInvoicePaymentStatus"`
}

func (c *Client) GetInvoiceStatus(ctx context.Context, paymentHash string) (InvoiceStatus, error) {
	query := `
		query InvoiceStatus($input: LnInvoicePaymentStatusInput!) {
			lnInvoicePaymentStatus(input: $input) {
				status
			}
		}
	`

	variables := map[string]interface{}{
		"input": map[string]interface{}{
			"paymentHash": paymentHash,
		},
	}

	var result invoiceStatusResponse
	if err := c.execute(ctx, query, variables, &result); err != nil {
		return "", err
	}

	return result.LnInvoicePaymentStatus.Status, nil
}
