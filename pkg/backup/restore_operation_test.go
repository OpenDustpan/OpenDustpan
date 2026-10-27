package backup_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/backup"
)

func TestParseRestoreID(t *testing.T) {
	t.Parallel()

	minted := backup.MintRestoreID()

	cases := []struct {
		name string
		in   string
		ok   bool
	}{
		{name: "a minted ID", in: minted.String(), ok: true},
		{name: "the lowercase base32 alphabet", in: "rst-abcdefghijklmnopqr27", ok: true},
		{name: "an empty ID", in: "", ok: false},
		{name: "a short ID", in: "rst-1", ok: false},
		{name: "a long ID", in: minted.String() + "a", ok: false},
		{name: "an uppercase letter", in: "rst-Abcdefghijklmnopqr27", ok: false},
		{name: "a digit outside base32", in: "rst-abcdefghijklmnopqr18", ok: false},
		{name: "another prefix", in: "bkp-abcdefghijklmnopqr27", ok: false},
		{name: "a path", in: "rst-../../../../etc/pass", ok: false},
		{name: "a quote", in: `rst-abcdefghijklmnopq"27`, ok: false},
		{name: "a line break", in: "rst-abcdefghijklmnopqr2\n", ok: false},
		{name: "a NUL byte", in: "rst-abcdefghijklmnopqr2\x00", ok: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := backup.ParseRestoreID(tc.in)

			if tc.ok && (err != nil || got.String() != tc.in) {
				t.Fatalf("ParseRestoreID(%q) = %q, %v, want the ID", tc.in, got, err)
			}

			if !tc.ok && !errors.Is(err, backup.ErrInvalidRestoreID) {
				t.Fatalf("ParseRestoreID(%q): error = %v, want ErrInvalidRestoreID", tc.in, err)
			}
		})
	}
}

func TestStagingArea(t *testing.T) {
	t.Parallel()

	id := backup.MintRestoreID()

	area, err := backup.NewStagingArea(id, 7)
	if err != nil {
		t.Fatalf("NewStagingArea: %v", err)
	}

	if got := area.String(); got != id.String()+".7" {
		t.Fatalf("String() = %q, want %q", got, id.String()+".7")
	}

	parsed, err := backup.ParseStagingArea(area.String())
	if err != nil || parsed != area {
		t.Fatalf("ParseStagingArea(%q) = %+v, %v, want %+v", area.String(), parsed, err, area)
	}
}

func TestStagingAreaRefuses(t *testing.T) {
	t.Parallel()

	id := backup.MintRestoreID().String()

	cases := []struct {
		name string
		in   string
	}{
		{name: "no token", in: id},
		{name: "an empty token", in: id + "."},
		{name: "a zero token", in: id + ".0"},
		{name: "a negative token", in: id + ".-1"},
		{name: "a token with a sign", in: id + ".+1"},
		{name: "a token with a leading zero", in: id + ".01"},
		{name: "a token too large", in: id + ".9223372036854775808"},
		{name: "a second separator", in: id + ".1.2"},
		{name: "an ID that is not valid", in: "rst-1.1"},
		{name: "a path", in: "../" + id + ".1"},
		{name: "a schema name", in: "restore_stage_" + strings.ReplaceAll(id, "-", "_") + ".1"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got, err := backup.ParseStagingArea(tc.in); !errors.Is(err, backup.ErrInvalidRestoreID) {
				t.Fatalf("ParseStagingArea(%q) = %+v, %v, want ErrInvalidRestoreID", tc.in, got, err)
			}
		})
	}
}

func TestRestoreSteps(t *testing.T) {
	t.Parallel()

	steps := backup.RestoreSteps()

	// The step names are stored in the step records of running operations,
	// so the list and its order are part of the operation version.
	want := []string{
		"stage", "check", "enter-maintenance", "pre-backup", "copy-blobs", "swap", "after-apply", "cleanup",
		"leave-maintenance", "finish",
	}

	if !slices.Equal(steps, want) {
		t.Fatalf("RestoreSteps() = %q, want %q", steps, want)
	}

	if got := backup.RestoreOperationKey("rst-a"); got != "restore:rst-a" {
		t.Fatalf("RestoreOperationKey = %q, want restore:rst-a", got)
	}
}
