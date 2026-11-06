package access

import (
	"encoding/base64"
	"errors"
	"math"
	"strconv"
	"strings"
	"testing"
)

// TestDecodeReviewCursorBounds proves that the cursor decoder rejects a number
// that is negative or that its target type cannot hold, and accepts the
// largest value of each number.
func TestDecodeReviewCursorBounds(t *testing.T) {
	t.Parallel()

	binding := strings.Repeat("a", 2*reviewBindingBytes)
	maxInt := strconv.Itoa(math.MaxInt)
	overInt := "9223372036854775808" // math.MaxInt64 + 1
	wide := strconv.FormatInt(math.MaxInt64, 10)

	tests := []struct {
		name    string
		numbers [4]string // as-of, key time, member time, bootstrap rows
		wantErr bool
	}{
		{name: "zero", numbers: [4]string{"0", "0", "0", "0"}, wantErr: false},
		{name: "largest bootstrap count", numbers: [4]string{"0", "0", "0", maxInt}, wantErr: false},
		{name: "largest times", numbers: [4]string{wide, wide, wide, "0"}, wantErr: false},
		{name: "bootstrap count above int64", numbers: [4]string{"0", "0", "0", overInt}, wantErr: true},
		{name: "as-of above int64", numbers: [4]string{overInt, "0", "0", "0"}, wantErr: true},
		{name: "negative bootstrap count", numbers: [4]string{"0", "0", "0", "-1"}, wantErr: true},
		{name: "negative time", numbers: [4]string{"0", "-1", "0", "0"}, wantErr: true},
		{name: "not a number", numbers: [4]string{"0", "0", "0", "1x"}, wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			n := tc.numbers
			raw := strings.Join([]string{n[0], n[1], "", n[2], "", n[3], binding}, "\x00")
			cursor := base64.RawURLEncoding.EncodeToString([]byte(raw))

			pos, err := decodeReviewCursor(cursor)
			if tc.wantErr {
				if !errors.Is(err, ErrInvalid) {
					t.Fatalf("decodeReviewCursor error = %v, want ErrInvalid", err)
				}

				return
			}

			if err != nil {
				t.Fatalf("decodeReviewCursor error = %v, want none", err)
			}

			want, convErr := strconv.Atoi(tc.numbers[3])
			if convErr != nil || pos.boot != want {
				t.Fatalf("boot = %d, want %d (%v)", pos.boot, want, convErr)
			}
		})
	}
}
