package session

import (
	"time"

	"github.com/google/uuid"
)

type Session struct {
	ID            uuid.UUID
	MacaroonID    string
	BalanceSats   int64
	NWCConnection *string
	Strikes       int
	Banned        bool
	CreatedAt     time.Time
	LastUsedAt    time.Time
}

type LedgerEntry struct {
	ID         uuid.UUID
	SessionID  uuid.UUID
	Type       string // "deposit" or "usage"
	AmountSats int64
	InvoiceID  *string
	Reference  *string
	CreatedAt  time.Time
}

type UsageLog struct {
	ID               uuid.UUID
	SessionID        uuid.UUID
	Model            string
	PromptTokens     int
	CompletionTokens int
	CostUSD          float64
	CostSats         int64
	CreatedAt        time.Time
}
