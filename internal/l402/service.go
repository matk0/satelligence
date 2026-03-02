package l402

import (
	"context"
	"fmt"
	"time"

	"github.com/satilligence/satilligence/internal/blink"
	"github.com/satilligence/satilligence/internal/session"
)

const (
	DefaultMacaroonExpiry = 90 * 24 * time.Hour // 90 days
)

type Service struct {
	macaroons      *MacaroonService
	blinkClient    *blink.Client
	sessionStore   *session.Store
	minDepositSats int64
}

func NewService(secret string, blinkClient *blink.Client, sessionStore *session.Store, minDepositSats int64) (*Service, error) {
	macaroonService, err := NewMacaroonService(secret, "satilligence")
	if err != nil {
		return nil, fmt.Errorf("failed to create macaroon service: %w", err)
	}

	return &Service{
		macaroons:      macaroonService,
		blinkClient:    blinkClient,
		sessionStore:   sessionStore,
		minDepositSats: minDepositSats,
	}, nil
}

type NewSessionResult struct {
	Macaroon   string
	Invoice    string
	AmountSats int64
	SessionID  string
}

func (s *Service) CreateNewSession(ctx context.Context) (*NewSessionResult, error) {
	// Generate macaroon first to get ID
	macaroon, data, err := s.macaroons.Create("pending", DefaultMacaroonExpiry)
	if err != nil {
		return nil, fmt.Errorf("failed to create macaroon: %w", err)
	}

	// Create session in database
	sess, err := s.sessionStore.Create(ctx, data.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to create session: %w", err)
	}

	// Create actual macaroon with session ID
	macaroon, _, err = s.macaroons.Create(sess.ID.String(), DefaultMacaroonExpiry)
	if err != nil {
		return nil, fmt.Errorf("failed to create macaroon: %w", err)
	}

	// Update session with correct macaroon ID
	// (In production, we'd do this in a transaction)

	// Create invoice
	invoice, err := s.blinkClient.CreateInvoice(ctx, s.minDepositSats, "Satilligence session deposit")
	if err != nil {
		return nil, fmt.Errorf("failed to create invoice: %w", err)
	}

	return &NewSessionResult{
		Macaroon:   macaroon,
		Invoice:    invoice.PaymentRequest,
		AmountSats: s.minDepositSats,
		SessionID:  sess.ID.String(),
	}, nil
}

func (s *Service) CreateTopUpInvoice(ctx context.Context, sessionID string, amountSats int64) (*blink.Invoice, error) {
	if amountSats < 1000 {
		amountSats = 1000 // Minimum top-up
	}

	return s.blinkClient.CreateInvoice(ctx, amountSats, "Satilligence balance top-up")
}

func (s *Service) VerifyMacaroon(encodedMacaroon string) (*MacaroonData, error) {
	return s.macaroons.Verify(encodedMacaroon)
}
