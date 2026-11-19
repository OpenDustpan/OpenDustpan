package storetest

import (
	"errors"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/stack"
)

// testRestoreStagingRefusesHostileTagValues stages a portable backup with one
// added tag row of an existing stack. A value that no port writes (a NUL, a
// right-to-left override, an escape, a carriage return, 257 bytes, invalid
// UTF-8) fails the stage with ErrCorrupt, the error text holds no value, and
// the live rows stay. A value with a line break and a tab stages. The
// backup is changed by hand, because no port writes such a value. A value that
// is not UTF-8 cannot be in a row of the portable format, which is JSON, so the
// SQLite internal test of the check covers it.
func testRestoreStagingRefusesHostileTagValues(t *testing.T, newStore RestoreFactory) {
	t.Helper()

	ps := portableRestoreStores(t, newStore(t), newStore(t))

	seedBackupStore(t, ps.src)

	prod := mustGetStack(t, ps.src, stack.Ref{Namespace: "other", Project: "web", Stack: "prod"})

	files, sum, descs := PortableFiles(t, ps.src)

	seedBackupStore(t, ps.dst)

	before := portableSummaryOf(t, ps.dst)

	cases := []struct {
		name   string
		value  string
		refuse bool
	}{
		{"a value with a line break and a tab", "a\nb\tc", false},
		{"a value of 256 bytes", strings.Repeat("v", 256), false},
		{"a value of 257 bytes", strings.Repeat("v", 257), true},
		{"a value of 100 KiB", strings.Repeat("v", 100<<10), true},
		{"a value with a NUL", "a\x00b", true},
		{"a value with U+202E", "a\u202eb", true},
		{"a value with an escape", "a\x1bb", true},
		{"a value with a carriage return", "a\rb", true},
	}

	for _, tc := range cases {
		row := map[string]any{"stack_id": prod.ID.String(), "name": "hostile", "value": tc.value}
		edited, editedDescs, editedSum := AddPortableRows(t, files, descs, sum, "stack_tags", row)

		_, stageErr := ps.dst.StagePortable(
			t.Context(), PortableOpener(edited), editedSum, editedDescs, StageAs(t, ps.dst),
		)

		switch {
		case tc.refuse && !errors.Is(stageErr, backup.ErrCorrupt):
			t.Fatalf("stage a backup with %s: err = %v, want ErrCorrupt", tc.name, stageErr)
		case tc.refuse && strings.Contains(stageErr.Error(), tc.value):
			t.Fatalf("stage a backup with %s: the error text holds the value", tc.name)
		case !tc.refuse && stageErr != nil:
			t.Fatalf("stage a backup with %s: %v", tc.name, stageErr)
		default:
		}

		assertSameSummary(t, portableSummaryOf(t, ps.dst), before)
		discardStaging(t, ps.dst)
	}
}
