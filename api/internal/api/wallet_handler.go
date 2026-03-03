package api

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/trandor/trandor/internal/lnbits"
)

// WalletHandler handles hosted wallet management endpoints
type WalletHandler struct {
	lnbitsClient *lnbits.Client
}

// NewWalletHandler creates a new wallet handler
func NewWalletHandler(lnbitsClient *lnbits.Client) *WalletHandler {
	return &WalletHandler{
		lnbitsClient: lnbitsClient,
	}
}

// CreateWalletRequest is the request body for creating a wallet
type CreateWalletRequest struct {
	Name string `json:"name"`
}

// CreateWalletResponse is the response from creating a wallet
type CreateWalletResponse struct {
	WalletID            string `json:"wallet_id"`
	NWCConnectionString string `json:"nwc_connection_string"`
	DepositLNURL        string `json:"deposit_lnurl,omitempty"`
	AdminKey            string `json:"admin_key"`
	InvoiceKey          string `json:"invoice_key"`
}

// WalletInfoResponse is the response from getting wallet info
type WalletInfoResponse struct {
	WalletID   string `json:"wallet_id"`
	Name       string `json:"name"`
	BalanceSats int64  `json:"balance_sats"`
}

// DepositRequest is the request body for creating a deposit invoice
type DepositRequest struct {
	AmountSats int64  `json:"amount_sats"`
	Memo       string `json:"memo"`
}

// DepositResponse is the response from creating a deposit invoice
type DepositResponse struct {
	PaymentRequest string `json:"payment_request"`
	PaymentHash    string `json:"payment_hash"`
}

// CreateWallet creates a new hosted wallet for an agent
// POST /v1/wallet/create
func (h *WalletHandler) CreateWallet(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Check if LNbits is configured
	if !h.lnbitsClient.IsConfigured() {
		writeError(w, http.StatusServiceUnavailable, "hosted_wallets_unavailable", "Hosted wallets are not available on this server")
		return
	}

	// Parse request
	var req CreateWalletRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		// Allow empty body, use default name
		req.Name = "Agent Wallet"
	}

	if req.Name == "" {
		req.Name = "Agent Wallet"
	}

	// Create user and wallet in LNbits
	user, err := h.lnbitsClient.CreateUser(ctx, req.Name, req.Name)
	if err != nil {
		slog.Error("failed to create LNbits user", "error", err)
		writeError(w, http.StatusInternalServerError, "wallet_creation_failed", "Failed to create wallet: "+err.Error())
		return
	}

	if len(user.Wallets) == 0 {
		writeError(w, http.StatusInternalServerError, "wallet_creation_failed", "No wallet created")
		return
	}

	wallet := user.Wallets[0]

	// Create NWC connection for the wallet
	nwcConn, err := h.lnbitsClient.CreateNWCConnection(ctx, wallet.AdminKey, req.Name+" NWC")
	if err != nil {
		slog.Error("failed to create NWC connection", "error", err)
		// Wallet was created but NWC failed - still return wallet info
		// The user can try creating NWC later or use the admin key directly
		writeJSON(w, http.StatusCreated, CreateWalletResponse{
			WalletID:            wallet.ID,
			NWCConnectionString: "",
			AdminKey:            wallet.AdminKey,
			InvoiceKey:          wallet.InvoiceKey,
		})
		return
	}

	slog.Info("created hosted wallet",
		"wallet_id", wallet.ID,
		"user_id", user.ID,
		"name", req.Name,
	)

	writeJSON(w, http.StatusCreated, CreateWalletResponse{
		WalletID:            wallet.ID,
		NWCConnectionString: nwcConn.PairingURL,
		AdminKey:            wallet.AdminKey,
		InvoiceKey:          wallet.InvoiceKey,
	})
}

// GetWallet returns wallet info and balance
// GET /v1/wallet/{wallet_id}
func (h *WalletHandler) GetWallet(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Check if LNbits is configured
	if !h.lnbitsClient.IsConfigured() {
		writeError(w, http.StatusServiceUnavailable, "hosted_wallets_unavailable", "Hosted wallets are not available on this server")
		return
	}

	// Get wallet admin key from header (required for authentication)
	adminKey := r.Header.Get("X-Admin-Key")
	if adminKey == "" {
		writeError(w, http.StatusUnauthorized, "missing_admin_key", "X-Admin-Key header required")
		return
	}

	// Get wallet info using admin key
	wallet, err := h.lnbitsClient.GetWallet(ctx, adminKey)
	if err != nil {
		slog.Error("failed to get wallet", "error", err)
		writeError(w, http.StatusNotFound, "wallet_not_found", "Wallet not found or invalid admin key")
		return
	}

	// Get balance
	balance, err := h.lnbitsClient.GetWalletBalance(ctx, adminKey)
	if err != nil {
		slog.Error("failed to get wallet balance", "error", err)
		balance = wallet.Balance / 1000 // Use cached balance if fetch fails
	}

	writeJSON(w, http.StatusOK, WalletInfoResponse{
		WalletID:    wallet.ID,
		Name:        wallet.Name,
		BalanceSats: balance,
	})
}

// CreateDeposit creates a Lightning invoice for depositing funds
// POST /v1/wallet/{wallet_id}/deposit
func (h *WalletHandler) CreateDeposit(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Check if LNbits is configured
	if !h.lnbitsClient.IsConfigured() {
		writeError(w, http.StatusServiceUnavailable, "hosted_wallets_unavailable", "Hosted wallets are not available on this server")
		return
	}

	walletID := chi.URLParam(r, "wallet_id")
	if walletID == "" {
		writeError(w, http.StatusBadRequest, "missing_wallet_id", "wallet_id is required")
		return
	}

	// Get invoice key from header (required for creating invoices)
	invoiceKey := r.Header.Get("X-Invoice-Key")
	if invoiceKey == "" {
		writeError(w, http.StatusUnauthorized, "missing_invoice_key", "X-Invoice-Key header required")
		return
	}

	// Parse request
	var req DepositRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "Failed to parse request body")
		return
	}

	if req.AmountSats <= 0 {
		writeError(w, http.StatusBadRequest, "invalid_amount", "amount_sats must be greater than 0")
		return
	}

	if req.Memo == "" {
		req.Memo = "Trandor wallet deposit"
	}

	// Create invoice
	invoice, err := h.lnbitsClient.CreateInvoice(ctx, invoiceKey, req.AmountSats, req.Memo)
	if err != nil {
		slog.Error("failed to create deposit invoice", "error", err, "wallet_id", walletID)
		writeError(w, http.StatusInternalServerError, "invoice_creation_failed", "Failed to create invoice: "+err.Error())
		return
	}

	slog.Info("created deposit invoice",
		"wallet_id", walletID,
		"amount_sats", req.AmountSats,
		"payment_hash", invoice.PaymentHash,
	)

	writeJSON(w, http.StatusCreated, DepositResponse{
		PaymentRequest: invoice.PaymentRequest,
		PaymentHash:    invoice.PaymentHash,
	})
}

// writeJSON writes a JSON response
func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}
