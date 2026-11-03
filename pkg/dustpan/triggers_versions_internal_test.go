package dustpan

import (
	"context"
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/graph"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
)

var errBoom = errors.New("boom")

// fakeReader is a job.FinalVersionReader with a fixed answer. It records the
// arguments of the last call.
type fakeReader struct {
	row   job.FinalVersion
	ok    bool
	err   error
	gotNS namespace.Name
	gotID string
}

func (f *fakeReader) FinalVersion(_ context.Context, ns namespace.Name, unitID string) (job.FinalVersion, bool, error) {
	f.gotNS = ns
	f.gotID = unitID

	return f.row, f.ok, f.err
}

func TestFinalVersionsLastFinal(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		fake    *fakeReader
		wantID  string
		wantOK  bool
		wantErr error
	}{
		{
			name: "recorded",
			fake: &fakeReader{
				row:   job.FinalVersion{UnitID: "", VersionID: "sv-9", Commit: "", Job: "", At: time.Time{}},
				ok:    true,
				err:   nil,
				gotNS: "",
				gotID: "",
			},
			wantID:  "sv-9",
			wantOK:  true,
			wantErr: nil,
		},
		{
			name: "none recorded",
			fake: &fakeReader{
				row:   job.FinalVersion{UnitID: "", VersionID: "", Commit: "", Job: "", At: time.Time{}},
				ok:    false,
				err:   nil,
				gotNS: "",
				gotID: "",
			},
			wantID:  "",
			wantOK:  false,
			wantErr: nil,
		},
		{
			name: "reader error",
			fake: &fakeReader{
				row:   job.FinalVersion{UnitID: "", VersionID: "", Commit: "", Job: "", At: time.Time{}},
				ok:    false,
				err:   errBoom,
				gotNS: "",
				gotID: "",
			},
			wantID:  "",
			wantOK:  false,
			wantErr: errBoom,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			v := finalVersions{reader: tc.fake}
			unit := graph.Unit{Namespace: "acme", Tool: "opentofu", ID: "u-1"}

			id, ok, err := v.LastFinal(t.Context(), unit)

			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("LastFinal error = %v, want %v", err, tc.wantErr)
			}

			if id != tc.wantID || ok != tc.wantOK {
				t.Fatalf("LastFinal = (%q, %t), want (%q, %t)", id, ok, tc.wantID, tc.wantOK)
			}

			if tc.fake.gotNS != "acme" || tc.fake.gotID != "u-1" {
				t.Fatalf("reader got (%q, %q), want (%q, %q)", tc.fake.gotNS, tc.fake.gotID, "acme", "u-1")
			}
		})
	}
}

func TestTriggerDispatcherVersionsOfJobStore(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		st      job.Store
		wantNil bool
	}{
		{
			name: "store with the port",
			st: struct {
				job.Store
				job.FinalVersionReader
			}{Store: nil, FinalVersionReader: nil},
			wantNil: false,
		},
		{
			name:    "store without the port",
			st:      struct{ job.Store }{Store: nil},
			wantNil: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cfg := new(settings)
			cfg.jobStore = tc.st
			got := triggerDispatcherConfig(cfg, &triggerPorts{records: nil, entries: nil}, triggerParts{
				starter: nil, savers: nil, units: nil, fences: nil,
			}).Versions

			if (got == nil) != tc.wantNil {
				t.Fatalf("Versions nil = %t, want %t", got == nil, tc.wantNil)
			}
		})
	}
}
