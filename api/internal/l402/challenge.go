package l402

import (
	"encoding/json"
	"net/http"
)

type Challenge struct {
	Macaroon   string `json:"macaroon"`
	Invoice    string `json:"invoice"`
	AmountSats int64  `json:"amount_sats"`
}

func WriteChallenge(w http.ResponseWriter, macaroon, invoice string, amountSats int64) {
	challenge := Challenge{
		Macaroon:   macaroon,
		Invoice:    invoice,
		AmountSats: amountSats,
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("WWW-Authenticate", "L402")
	w.WriteHeader(http.StatusPaymentRequired)
	json.NewEncoder(w).Encode(challenge)
}

type ErrorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message,omitempty"`
}

func WriteError(w http.ResponseWriter, statusCode int, err string, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(ErrorResponse{
		Error:   err,
		Message: message,
	})
}
