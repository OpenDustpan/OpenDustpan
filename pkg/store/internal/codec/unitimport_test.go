package codec_test

import (
	"slices"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/unitimport"
	"opendustpan.org/opendustpan/pkg/history"
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
)

func TestPlanRoundTrip(t *testing.T) {
	t.Parallel()

	empty := unitimport.Plan{
		At: time.Time{}, Units: 0, Versions: 0, Bytes: 0, Capabilities: nil, Findings: nil, Requests: nil,
		Reservations: nil,
	}
	full := unitimport.Plan{
		At:           time.UnixMilli(1_800_000_000_000).UTC(),
		Units:        2,
		Versions:     9,
		Bytes:        4096,
		Capabilities: []unitimport.Capability{unitimport.CapabilityDecrypt, unitimport.CapabilityLock},
		Findings:     []unitimport.Finding{{Code: "a", Severity: unitimport.SeverityWarning, Message: "m", Help: "h"}},
		Requests:     []string{"sample decrypt"},
		Reservations: []string{"res-1", "res-2"},
	}

	for name, plan := range map[string]unitimport.Plan{"empty": empty, "full": full} {
		text, err := codec.EncodePlan(plan)
		if err != nil {
			t.Fatalf("%s: encode: %v", name, err)
		}

		got, err := codec.DecodePlan(text)
		if err != nil {
			t.Fatalf("%s: decode %q: %v", name, text, err)
		}

		same := samePlan(got, plan)
		if !same {
			t.Errorf("%s: plan = %+v, want %+v", name, got, plan)
		}
	}

	if _, err := codec.DecodePlan("{"); err == nil {
		t.Error("decode of a bad plan succeeded, want an error")
	}
}

func TestImportUnitRowTarget(t *testing.T) {
	t.Parallel()

	u := unitimport.ImportUnit{
		ID: "u-1", ImportID: "imp-1", Source: unitimport.SourceUnit{Tool: "opentofu", Name: "web"}, TargetName: "web",
		Target: history.Unit{Namespace: "", Tool: "", ID: ""}, State: unitimport.UnitPlanned, ErrorCode: "",
		Versions: 1, Committed: 0, Bytes: 10, Encryption: "", Findings: nil,
	}

	row, err := codec.ImportUnitToRow("ns-id", "imp-1", 3, u)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	if row.Position != 3 || row.TargetUnitID != "" {
		t.Fatalf("row = %+v, want position 3 and no target", row)
	}

	got, err := codec.ImportUnitFromRow("acme", row)
	if err != nil || got.Target != (history.Unit{Namespace: "", Tool: "", ID: ""}) {
		t.Fatalf("unit = %+v, %v, want no target", got, err)
	}

	row.TargetTool, row.TargetUnitID = "opentofu", "ws-1"

	got, err = codec.ImportUnitFromRow("acme", row)
	if want := (history.Unit{Namespace: "acme", Tool: "opentofu", ID: "ws-1"}); err != nil || got.Target != want {
		t.Fatalf("unit target = %+v, %v, want %+v", got.Target, err, want)
	}
}

func TestLedgerRowRoundTrip(t *testing.T) {
	t.Parallel()

	r := unitimport.LedgerRow{
		ImportID: "imp-1", ImportUnitID: "u-1", Version: "v1", SourceSHA256: "a", StoredSHA256: "b",
		Item: history.ItemRef{
			Unit: history.Unit{
				Namespace: "acme",
				Tool:      "opentofu",
				ID:        "ws-1",
			},
			Kind: secret.ItemKindState,
			Item: "i-1",
		},
		CommittedAt: time.UnixMilli(1_800_000_000_000).UTC(),
	}

	got := codec.LedgerFromRow("acme", codec.LedgerToRow("ns-id", r))
	if got != r {
		t.Errorf("ledger row = %+v, want %+v", got, r)
	}
}

func samePlan(got, want unitimport.Plan) bool {
	return got.At.Equal(want.At) && got.Units == want.Units && got.Versions == want.Versions &&
		got.Bytes == want.Bytes && slices.Equal(got.Capabilities, want.Capabilities) &&
		slices.Equal(got.Findings, want.Findings) && slices.Equal(got.Requests, want.Requests) &&
		slices.Equal(got.Reservations, want.Reservations)
}
