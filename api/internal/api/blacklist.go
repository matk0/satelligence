package api

import (
	"encoding/json"
	"log/slog"
	"os"
	"sync"
	"time"
)

// WalletBlacklist manages a list of blacklisted wallet pubkeys
type WalletBlacklist struct {
	mu       sync.RWMutex
	wallets  map[string]time.Time // pubkey -> blacklisted_at
	filePath string
}

// BlacklistEntry represents a single blacklist entry for JSON serialization
type BlacklistEntry struct {
	Pubkey       string    `json:"pubkey"`
	BlacklistedAt time.Time `json:"blacklisted_at"`
}

// NewWalletBlacklist creates a new WalletBlacklist instance
func NewWalletBlacklist(filePath string) *WalletBlacklist {
	return &WalletBlacklist{
		wallets:  make(map[string]time.Time),
		filePath: filePath,
	}
}

// IsBlacklisted checks if a wallet pubkey is blacklisted
func (b *WalletBlacklist) IsBlacklisted(pubkey string) bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	_, exists := b.wallets[pubkey]
	return exists
}

// Add adds a wallet pubkey to the blacklist
func (b *WalletBlacklist) Add(pubkey string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if _, exists := b.wallets[pubkey]; exists {
		return // Already blacklisted
	}

	b.wallets[pubkey] = time.Now()
	slog.Warn("wallet blacklisted", "pubkey", pubkey)

	// Save asynchronously to not block the caller
	go func() {
		if err := b.save(); err != nil {
			slog.Error("failed to save blacklist", "error", err)
		}
	}()
}

// Remove removes a wallet pubkey from the blacklist
func (b *WalletBlacklist) Remove(pubkey string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	if _, exists := b.wallets[pubkey]; !exists {
		return false // Not blacklisted
	}

	delete(b.wallets, pubkey)
	slog.Info("wallet removed from blacklist", "pubkey", pubkey)

	// Save asynchronously to not block the caller
	go func() {
		if err := b.save(); err != nil {
			slog.Error("failed to save blacklist after removal", "error", err)
		}
	}()

	return true
}

// Load loads the blacklist from the file
func (b *WalletBlacklist) Load() error {
	b.mu.Lock()
	defer b.mu.Unlock()

	data, err := os.ReadFile(b.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			// File doesn't exist, start with empty blacklist
			slog.Info("blacklist file not found, starting fresh", "path", b.filePath)
			return nil
		}
		return err
	}

	var entries []BlacklistEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return err
	}

	b.wallets = make(map[string]time.Time)
	for _, entry := range entries {
		b.wallets[entry.Pubkey] = entry.BlacklistedAt
	}

	slog.Info("blacklist loaded", "count", len(b.wallets), "path", b.filePath)
	return nil
}

// Save saves the blacklist to the file
func (b *WalletBlacklist) Save() error {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.save()
}

// save is the internal save method (must be called with lock held)
func (b *WalletBlacklist) save() error {
	entries := make([]BlacklistEntry, 0, len(b.wallets))
	for pubkey, blacklistedAt := range b.wallets {
		entries = append(entries, BlacklistEntry{
			Pubkey:       pubkey,
			BlacklistedAt: blacklistedAt,
		})
	}

	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(b.filePath, data, 0644)
}

// Count returns the number of blacklisted wallets
func (b *WalletBlacklist) Count() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.wallets)
}
