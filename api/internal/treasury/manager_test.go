package treasury

import (
	"testing"
	"time"

	"github.com/trandor/trandor/config"
)

func TestMetrics_RecordBalances(t *testing.T) {
	m := NewMetrics()

	m.RecordBalances(10000, 5000)

	snap := m.GetSnapshot()
	if snap.BTCBalanceSats != 10000 {
		t.Errorf("expected BTCBalanceSats=10000, got %d", snap.BTCBalanceSats)
	}
	if snap.USDBalanceCents != 5000 {
		t.Errorf("expected USDBalanceCents=5000, got %d", snap.USDBalanceCents)
	}
}

func TestMetrics_RecordConversion(t *testing.T) {
	m := NewMetrics()

	m.RecordConversion(1000)
	m.RecordConversion(2000)

	snap := m.GetSnapshot()
	if snap.ConversionsTotal != 2 {
		t.Errorf("expected ConversionsTotal=2, got %d", snap.ConversionsTotal)
	}
	if snap.SatsConverted != 3000 {
		t.Errorf("expected SatsConverted=3000, got %d", snap.SatsConverted)
	}
}

func TestMetrics_RecordError(t *testing.T) {
	m := NewMetrics()

	m.RecordError()
	m.RecordError()
	m.RecordError()

	snap := m.GetSnapshot()
	if snap.ErrorsTotal != 3 {
		t.Errorf("expected ErrorsTotal=3, got %d", snap.ErrorsTotal)
	}
}

func TestManager_GetStatus_Disabled(t *testing.T) {
	cfg := config.TreasuryConfig{
		Enabled:       false,
		ThresholdSats: 1000,
		MinSats:       500,
		Interval:      5 * time.Minute,
		RetainBuffer:  2000,
	}

	// nil client is fine when disabled
	mgr := NewManager(nil, cfg)

	status := mgr.GetStatus()
	if status.Enabled {
		t.Error("expected Enabled=false")
	}
	if status.ThresholdSats != 1000 {
		t.Errorf("expected ThresholdSats=1000, got %d", status.ThresholdSats)
	}
	if status.RetainBuffer != 2000 {
		t.Errorf("expected RetainBuffer=2000, got %d", status.RetainBuffer)
	}
}

func TestManager_ConversionLogic(t *testing.T) {
	// Test the conversion calculation logic
	tests := []struct {
		name           string
		btcBalance     int64
		threshold      int64
		retainBuffer   int64
		minSats        int64
		expectConvert  int64
		shouldConvert  bool
	}{
		{
			name:          "below threshold - no conversion",
			btcBalance:    500,
			threshold:     1000,
			retainBuffer:  200,
			minSats:       100,
			shouldConvert: false,
		},
		{
			name:           "above threshold - convert excess",
			btcBalance:     2000,
			threshold:      1000,
			retainBuffer:   200,
			minSats:        100,
			expectConvert:  1000, // min(available=1800, excess=1000) = 1000
			shouldConvert:  true,
		},
		{
			name:          "above threshold but below min - no conversion",
			btcBalance:    1050,
			threshold:     1000,
			retainBuffer:  200,
			minSats:       100,
			shouldConvert: false, // excess=50 < minSats=100
		},
		{
			name:           "buffer limits conversion",
			btcBalance:     1500,
			threshold:      1000,
			retainBuffer:   1000,
			minSats:        100,
			expectConvert:  500, // min(available=500, excess=500) = 500
			shouldConvert:  true,
		},
		{
			name:          "all in buffer - no conversion",
			btcBalance:    1500,
			threshold:     1000,
			retainBuffer:  2000,
			minSats:       100,
			shouldConvert: false, // available = -500, no conversion
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Simulate the conversion logic from checkAndConvert
			available := tt.btcBalance - tt.retainBuffer
			if available <= 0 {
				if tt.shouldConvert {
					t.Error("expected conversion but available <= 0")
				}
				return
			}

			excess := tt.btcBalance - tt.threshold
			if excess <= 0 {
				if tt.shouldConvert {
					t.Error("expected conversion but excess <= 0")
				}
				return
			}

			toConvert := min(available, excess)
			if toConvert < tt.minSats {
				if tt.shouldConvert {
					t.Error("expected conversion but toConvert < minSats")
				}
				return
			}

			if !tt.shouldConvert {
				t.Error("expected no conversion but got one")
				return
			}

			if toConvert != tt.expectConvert {
				t.Errorf("expected toConvert=%d, got %d", tt.expectConvert, toConvert)
			}
		})
	}
}
