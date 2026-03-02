package l402

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"gopkg.in/macaroon.v2"
)

var (
	ErrInvalidMacaroon = errors.New("invalid macaroon")
	ErrExpiredMacaroon = errors.New("macaroon expired")
	ErrInvalidPreimage = errors.New("invalid preimage")
)

type MacaroonService struct {
	secret   []byte
	location string
}

func NewMacaroonService(secretHex string, location string) (*MacaroonService, error) {
	secret, err := hex.DecodeString(secretHex)
	if err != nil {
		return nil, fmt.Errorf("invalid secret hex: %w", err)
	}

	return &MacaroonService{
		secret:   secret,
		location: location,
	}, nil
}

type MacaroonData struct {
	ID        string
	SessionID string
	ExpiresAt time.Time
}

func (s *MacaroonService) Create(sessionID string, expiresIn time.Duration) (string, *MacaroonData, error) {
	// Generate random ID
	idBytes := make([]byte, 16)
	if _, err := rand.Read(idBytes); err != nil {
		return "", nil, fmt.Errorf("failed to generate ID: %w", err)
	}
	id := hex.EncodeToString(idBytes)

	// Create macaroon
	m, err := macaroon.New(s.secret, []byte(id), s.location, macaroon.LatestVersion)
	if err != nil {
		return "", nil, fmt.Errorf("failed to create macaroon: %w", err)
	}

	// Add caveats
	expiresAt := time.Now().Add(expiresIn)
	if err := m.AddFirstPartyCaveat([]byte(fmt.Sprintf("session_id = %s", sessionID))); err != nil {
		return "", nil, fmt.Errorf("failed to add session caveat: %w", err)
	}
	if err := m.AddFirstPartyCaveat([]byte(fmt.Sprintf("expires = %d", expiresAt.Unix()))); err != nil {
		return "", nil, fmt.Errorf("failed to add expiry caveat: %w", err)
	}

	// Encode to base64
	macBytes, err := m.MarshalBinary()
	if err != nil {
		return "", nil, fmt.Errorf("failed to marshal macaroon: %w", err)
	}

	encoded := base64.StdEncoding.EncodeToString(macBytes)

	return encoded, &MacaroonData{
		ID:        id,
		SessionID: sessionID,
		ExpiresAt: expiresAt,
	}, nil
}

func (s *MacaroonService) Verify(encodedMacaroon string) (*MacaroonData, error) {
	macBytes, err := base64.StdEncoding.DecodeString(encodedMacaroon)
	if err != nil {
		return nil, ErrInvalidMacaroon
	}

	var m macaroon.Macaroon
	if err := m.UnmarshalBinary(macBytes); err != nil {
		return nil, ErrInvalidMacaroon
	}

	// Verify signature
	var data MacaroonData
	data.ID = string(m.Id())

	// Parse and verify caveats
	for _, caveat := range m.Caveats() {
		caveatStr := string(caveat.Id)

		if strings.HasPrefix(caveatStr, "session_id = ") {
			data.SessionID = strings.TrimPrefix(caveatStr, "session_id = ")
		} else if strings.HasPrefix(caveatStr, "expires = ") {
			var expiresUnix int64
			if _, err := fmt.Sscanf(caveatStr, "expires = %d", &expiresUnix); err == nil {
				data.ExpiresAt = time.Unix(expiresUnix, 0)
			}
		}
	}

	// Check expiration
	if !data.ExpiresAt.IsZero() && time.Now().After(data.ExpiresAt) {
		return nil, ErrExpiredMacaroon
	}

	// Verify macaroon signature using the verifier
	verifier := func(caveat string) error {
		// All our caveats are valid if we parsed them
		if strings.HasPrefix(caveat, "session_id = ") ||
			strings.HasPrefix(caveat, "expires = ") {
			return nil
		}
		return fmt.Errorf("unknown caveat: %s", caveat)
	}

	err = m.Verify(s.secret, verifier, nil)
	if err != nil {
		return nil, ErrInvalidMacaroon
	}

	return &data, nil
}
