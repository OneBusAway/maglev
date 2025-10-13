package scorers

import (
	"github.com/OneBusAway/go-gtfs"
	"testing"
)

func TestTransferScorer_InvalidTypes(t *testing.T) {
	scorer := &TransferScorer{}

	tests := []struct {
		name string
		a    interface{}
		b    interface{}
		want float64
	}{
		{
			name: "nil values",
			a:    nil,
			b:    nil,
			want: 0.0,
		},
		{
			name: "first is not Transfer",
			a:    "not a transfer",
			b:    &gtfs.Transfer{},
			want: 0.0,
		},
		{
			name: "second is not Transfer",
			a:    &gtfs.Transfer{},
			b:    123,
			want: 0.0,
		},
		{
			name: "both wrong types",
			a:    &gtfs.Stop{},
			b:    &gtfs.Route{},
			want: 0.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := scorer.Score(tt.a, tt.b)
			if got != tt.want {
				t.Errorf("Score() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTransferScorer_StopMatching(t *testing.T) {
	scorer := &TransferScorer{}

	stopA := &gtfs.Stop{Id: "stop_a"}
	stopB := &gtfs.Stop{Id: "stop_b"}
	stopC := &gtfs.Stop{Id: "stop_c"}

	tests := []struct {
		name      string
		transferA *gtfs.Transfer
		transferB *gtfs.Transfer
		expected  float64
	}{
		{
			name: "matching from and to stops",
			transferA: &gtfs.Transfer{
				From: stopA,
				To:   stopB,
			},
			transferB: &gtfs.Transfer{
				From: stopA,
				To:   stopB,
			},
			expected: 1.0,
		},
		{
			name: "different from stop",
			transferA: &gtfs.Transfer{
				From: stopA,
				To:   stopB,
			},
			transferB: &gtfs.Transfer{
				From: stopC,
				To:   stopB,
			},
			expected: 0.0,
		},
		{
			name: "different to stop",
			transferA: &gtfs.Transfer{
				From: stopA,
				To:   stopB,
			},
			transferB: &gtfs.Transfer{
				From: stopA,
				To:   stopC,
			},
			expected: 0.0,
		},
		{
			name: "completely different stops",
			transferA: &gtfs.Transfer{
				From: stopA,
				To:   stopB,
			},
			transferB: &gtfs.Transfer{
				From: stopC,
				To:   stopC,
			},
			expected: 0.0,
		},
		{
			name: "nil from stop",
			transferA: &gtfs.Transfer{
				From: nil,
				To:   stopB,
			},
			transferB: &gtfs.Transfer{
				From: stopA,
				To:   stopB,
			},
			expected: 0.0,
		},
		{
			name: "nil to stop",
			transferA: &gtfs.Transfer{
				From: stopA,
				To:   nil,
			},
			transferB: &gtfs.Transfer{
				From: stopA,
				To:   stopB,
			},
			expected: 0.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := scorer.Score(tt.transferA, tt.transferB)
			if got != tt.expected {
				t.Errorf("Score() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestTransferScorer_TransferTypeMatching(t *testing.T) {
	scorer := &TransferScorer{}

	stopA := &gtfs.Stop{Id: "stop_a"}
	stopB := &gtfs.Stop{Id: "stop_b"}

	tests := []struct {
		name      string
		transferA *gtfs.Transfer
		transferB *gtfs.Transfer
		expected  float64
	}{
		{
			name: "matching transfer type",
			transferA: &gtfs.Transfer{
				From: stopA,
				To:   stopB,
				Type: gtfs.TransferType_Timed,
			},
			transferB: &gtfs.Transfer{
				From: stopA,
				To:   stopB,
				Type: gtfs.TransferType_Timed,
			},
			expected: 1.0,
		},
		{
			name: "different transfer type",
			transferA: &gtfs.Transfer{
				From: stopA,
				To:   stopB,
				Type: gtfs.TransferType_Timed,
			},
			transferB: &gtfs.Transfer{
				From: stopA,
				To:   stopB,
				Type: gtfs.TransferType_NotPossible,
			},
			expected: 0.5, // Stops match, type doesn't
		},
		{
			name: "zero type (recommended)",
			transferA: &gtfs.Transfer{
				From: stopA,
				To:   stopB,
				Type: gtfs.TransferType_Recommended,
			},
			transferB: &gtfs.Transfer{
				From: stopA,
				To:   stopB,
				Type: gtfs.TransferType_Recommended,
			},
			expected: 1.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := scorer.Score(tt.transferA, tt.transferB)
			if got != tt.expected {
				t.Errorf("Score() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestTransferScorer_MinTransferTimeMatching(t *testing.T) {
	scorer := &TransferScorer{}

	stopA := &gtfs.Stop{Id: "stop_a"}
	stopB := &gtfs.Stop{Id: "stop_b"}

	minTime120 := int32(120)
	minTime180 := int32(180)

	tests := []struct {
		name      string
		transferA *gtfs.Transfer
		transferB *gtfs.Transfer
		expected  float64
	}{
		{
			name: "matching min_transfer_time",
			transferA: &gtfs.Transfer{
				From:            stopA,
				To:              stopB,
				Type:            gtfs.TransferType_Timed,
				MinTransferTime: &minTime120,
			},
			transferB: &gtfs.Transfer{
				From:            stopA,
				To:              stopB,
				Type:            gtfs.TransferType_Timed,
				MinTransferTime: &minTime120,
			},
			expected: 1.0,
		},
		{
			name: "different min_transfer_time",
			transferA: &gtfs.Transfer{
				From:            stopA,
				To:              stopB,
				Type:            gtfs.TransferType_Timed,
				MinTransferTime: &minTime120,
			},
			transferB: &gtfs.Transfer{
				From:            stopA,
				To:              stopB,
				Type:            gtfs.TransferType_Timed,
				MinTransferTime: &minTime180,
			},
			expected: 2.0 / 3.0, // Stops match, type matches, time doesn't
		},
		{
			name: "nil min_transfer_time ignored",
			transferA: &gtfs.Transfer{
				From:            stopA,
				To:              stopB,
				Type:            gtfs.TransferType_Timed,
				MinTransferTime: nil,
			},
			transferB: &gtfs.Transfer{
				From:            stopA,
				To:              stopB,
				Type:            gtfs.TransferType_Timed,
				MinTransferTime: nil,
			},
			expected: 1.0, // Stops and type match, time is ignored
		},
		{
			name: "one nil min_transfer_time ignored",
			transferA: &gtfs.Transfer{
				From:            stopA,
				To:              stopB,
				Type:            gtfs.TransferType_Timed,
				MinTransferTime: &minTime120,
			},
			transferB: &gtfs.Transfer{
				From:            stopA,
				To:              stopB,
				Type:            gtfs.TransferType_Timed,
				MinTransferTime: nil,
			},
			expected: 1.0, // Stops and type match, time is ignored
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := scorer.Score(tt.transferA, tt.transferB)
			if got != tt.expected {
				t.Errorf("Score() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestTransferScorer_CompositeScoring(t *testing.T) {
	scorer := &TransferScorer{}

	stopA := &gtfs.Stop{Id: "stop_a"}
	stopB := &gtfs.Stop{Id: "stop_b"}

	minTime120 := int32(120)

	tests := []struct {
		name      string
		transferA *gtfs.Transfer
		transferB *gtfs.Transfer
		expected  float64
	}{
		{
			name: "all fields match",
			transferA: &gtfs.Transfer{
				From:            stopA,
				To:              stopB,
				Type:            gtfs.TransferType_Timed,
				MinTransferTime: &minTime120,
			},
			transferB: &gtfs.Transfer{
				From:            stopA,
				To:              stopB,
				Type:            gtfs.TransferType_Timed,
				MinTransferTime: &minTime120,
			},
			expected: 1.0,
		},
		{
			name: "stops and type match, no min_transfer_time",
			transferA: &gtfs.Transfer{
				From:            stopA,
				To:              stopB,
				Type:            gtfs.TransferType_Timed,
				MinTransferTime: nil,
			},
			transferB: &gtfs.Transfer{
				From:            stopA,
				To:              stopB,
				Type:            gtfs.TransferType_Timed,
				MinTransferTime: nil,
			},
			expected: 1.0,
		},
		{
			name: "only stops match",
			transferA: &gtfs.Transfer{
				From:            stopA,
				To:              stopB,
				Type:            gtfs.TransferType_Timed,
				MinTransferTime: &minTime120,
			},
			transferB: &gtfs.Transfer{
				From:            stopA,
				To:              stopB,
				Type:            gtfs.TransferType_NotPossible,
				MinTransferTime: nil,
			},
			expected: 0.5, // Stops match, type doesn't, time ignored
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := scorer.Score(tt.transferA, tt.transferB)
			if got != tt.expected {
				t.Errorf("Score() = %v, want %v", got, tt.expected)
			}
		})
	}
}
