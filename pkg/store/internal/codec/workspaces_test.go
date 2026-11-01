package codec_test

import (
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/store/internal/codec"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// TestFromWorkspaceRowReleaseKind proves the read of the record of the last
// release alone: a row whose kind is not normal or forced reads as no record,
// whatever its other columns hold, so it never matches a repeated request. A
// valid kind reads back with each column of the record.
func TestFromWorkspaceRowReleaseKind(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, time.November, 1, 9, 0, 0, 0, time.UTC)

	cases := []struct {
		name string
		kind string
		want workspace.Release
	}{
		{name: "no record", kind: "", want: workspace.NoRelease()},
		{
			name: "normal",
			kind: "normal",
			want: workspace.Release{
				At:     at,
				LockID: "lock-a",
				Holder: "bob",
				By:     "alice",
				Kind:   workspace.ReleaseNormal,
			},
		},
		{
			name: "forced",
			kind: "forced",
			want: workspace.Release{
				At:     at,
				LockID: "lock-a",
				Holder: "bob",
				By:     "alice",
				Kind:   workspace.ReleaseForced,
			},
		},
		{name: "unknown", kind: "expired", want: workspace.NoRelease()},
		{name: "upper case", kind: "Normal", want: workspace.NoRelease()},
		{name: "trailing space", kind: "forced ", want: workspace.NoRelease()},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			row := codec.WorkspaceRow{
				Namespace: "acme", NamespaceID: "", Name: "web", ID: "ws-1", LockID: "", LockHolder: "",
				LockReason: "", CurrentStateVersion: "", Revision: 2, LockedAtMs: 0, CreatedAtMs: 1, UpdatedAtMs: 2,
				AllowForcedWrites: false, AllowUnlockedWrites: false, LastReleaseLockID: "lock-a",
				LastReleaseHolder: "bob", LastReleaseBy: "alice", LastReleaseKind: tc.kind,
				LastReleaseAtMs: at.UnixMilli(),
			}

			got := codec.FromWorkspaceRow(row).LastRelease
			if !got.At.Equal(tc.want.At) || got.LockID != tc.want.LockID || got.Holder != tc.want.Holder ||
				got.By != tc.want.By || got.Kind != tc.want.Kind {
				t.Fatalf("record = %+v, want %+v", got, tc.want)
			}

			if back := codec.ToWorkspaceRow(codec.FromWorkspaceRow(row)); tc.want.Recorded() && back != row {
				t.Fatalf("round trip = %+v, want %+v", back, row)
			}
		})
	}
}
