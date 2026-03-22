package ads

import (
	"testing"
	"time"
)

func TestDurationToAdsTicks(t *testing.T) {
	tests := []struct {
		name       string
		in         time.Duration
		defaultVal time.Duration
		want       uint32
		wantErr    bool
	}{
		{name: "default used", in: 0, defaultVal: 100 * time.Millisecond, want: 1_000_000},
		{name: "one millisecond", in: 1 * time.Millisecond, defaultVal: 100 * time.Millisecond, want: 10_000},
		{name: "sub tick rounds up", in: 50 * time.Nanosecond, defaultVal: 100 * time.Millisecond, want: 1},
		{name: "negative rejected", in: -1 * time.Millisecond, defaultVal: 100 * time.Millisecond, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := durationToAdsTicks("v", tt.in, tt.defaultVal)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("got %d, want %d", got, tt.want)
			}
		})
	}
}
