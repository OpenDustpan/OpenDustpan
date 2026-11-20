package backup_test

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/backup"
)

// TestDecodeRestoreSwapRefusesHostileResults checks that DecodeRestoreSwap
// returns ErrCorrupt, and a zero result, for a step result that
// EncodeRestoreSwap did not make.
func TestDecodeRestoreSwapRefusesHostileResults(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		result string
	}{
		{name: "negative expired update leases", result: `{"expiredUpdateLeases":-1}`},
		{name: "removed dropped temp admins count", result: `{"droppedTempAdmins":0,"expiredUpdateLeases":0}`},
		{name: "unknown field", result: `{"expiredUpdateLeases":1,"extra":1}`},
		{name: "unknown field only", result: `{"expiredLeases":3}`},
		{name: "text for a count", result: `{"expiredUpdateLeases":"1"}`},
		{name: "not an object", result: `[1,2]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := backup.DecodeRestoreSwap([]byte(tc.result))
			if !errors.Is(err, backup.ErrCorrupt) {
				t.Fatalf("decode %s: error = %v, want ErrCorrupt", tc.result, err)
			}

			if got != (backup.KeepAccessResult{ExpiredUpdateLeases: 0}) {
				t.Errorf("decode %s: result = %+v, want the zero result", tc.result, got)
			}
		})
	}
}

// TestRestoreSwapResultRoundTrips checks that the counts of a swap survive the
// step result.
func TestRestoreSwapResultRoundTrips(t *testing.T) {
	t.Parallel()

	want := backup.KeepAccessResult{ExpiredUpdateLeases: 2}

	got, err := backup.DecodeRestoreSwap(backup.EncodeRestoreSwap(want, backup.PlainSwap(false)))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	if got != want {
		t.Errorf("decoded result = %+v, want %+v", got, want)
	}
}
