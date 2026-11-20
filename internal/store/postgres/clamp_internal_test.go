package postgres

import (
	"math"
	"testing"
)

// TestClampInt32 proves that clampInt32 keeps a value inside 0 to MaxInt32,
// so that no input wraps when it becomes a PostgreSQL integer parameter.
func TestClampInt32(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   int
		want int32
	}{
		{name: "zero", in: 0, want: 0},
		{name: "one", in: 1, want: 1},
		{name: "max int32", in: math.MaxInt32, want: math.MaxInt32},
		{name: "max int32 plus one", in: math.MaxInt32 + 1, want: math.MaxInt32},
		{name: "max int", in: math.MaxInt, want: math.MaxInt32},
		{name: "minus one", in: -1, want: 0},
		{name: "min int", in: math.MinInt, want: 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := clampInt32(tc.in); got != tc.want {
				t.Fatalf("clampInt32(%d) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}
