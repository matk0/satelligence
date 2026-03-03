package blink

import (
	"context"
	"testing"
)

func TestClient_DevMode_GetWalletBalances(t *testing.T) {
	// No API key = development mode
	client := NewClient("")

	balances, err := client.GetWalletBalances(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Dev mode returns mock values
	if balances.BTCSats != 10000 {
		t.Errorf("expected BTCSats=10000, got %d", balances.BTCSats)
	}
	if balances.USDCents != 5000 {
		t.Errorf("expected USDCents=5000, got %d", balances.USDCents)
	}
}

func TestClient_DevMode_TransferBTCToUSD(t *testing.T) {
	// No API key = development mode
	client := NewClient("")

	result, err := client.TransferBTCToUSD(context.Background(), 1000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Status != "SUCCESS" {
		t.Errorf("expected Status=SUCCESS, got %s", result.Status)
	}
	if result.TransactionID != "dev_transfer_id" {
		t.Errorf("expected TransactionID=dev_transfer_id, got %s", result.TransactionID)
	}
}

func TestClient_GetWalletID(t *testing.T) {
	client := &Client{
		btcWalletID: "btc-123",
		usdWalletID: "usd-456",
	}

	if client.GetWalletID() != "btc-123" {
		t.Errorf("expected GetWalletID()=btc-123, got %s", client.GetWalletID())
	}
	if client.GetBTCWalletID() != "btc-123" {
		t.Errorf("expected GetBTCWalletID()=btc-123, got %s", client.GetBTCWalletID())
	}
	if client.GetUSDWalletID() != "usd-456" {
		t.Errorf("expected GetUSDWalletID()=usd-456, got %s", client.GetUSDWalletID())
	}
}

func TestClient_TransferBTCToUSD_NoUSDWallet(t *testing.T) {
	client := &Client{
		apiKey:      "test-key",
		btcWalletID: "btc-123",
		usdWalletID: "", // No USD wallet
	}

	_, err := client.TransferBTCToUSD(context.Background(), 1000)
	if err == nil {
		t.Error("expected error when USD wallet not configured")
	}
	if err.Error() != "USD wallet not configured" {
		t.Errorf("unexpected error message: %v", err)
	}
}
