package drift_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/drift"
	"opendustpan.org/opendustpan/pkg/graph"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
)

// mustValid fails the test when a Validate call returned an error.
func mustValid(t *testing.T, err error) {
	t.Helper()

	if err != nil {
		t.Fatalf("Validate() = %v, want nil", err)
	}
}

// mustInvalid fails the test unless a Validate call returned an error that
// wraps ErrInvalid.
func mustInvalid(t *testing.T, err error) {
	t.Helper()

	if !errors.Is(err, drift.ErrInvalid) {
		t.Fatalf("Validate() = %v, want an error that wraps ErrInvalid", err)
	}
}

func unit() graph.Unit {
	return graph.Unit{Namespace: "acme", Tool: "opentofu", ID: "wsp-1"}
}

func result(resources ...drift.Resource) drift.Result {
	return drift.Result{
		CheckedAt: time.UnixMilli(1_800_000_000_000).UTC(),
		Unit:      unit(),
		JobID:     "job-1",
		Resources: resources,
		Truncated: false,
	}
}

func changed(address string) drift.Resource {
	return drift.Resource{Address: address, Kind: drift.KindChanged}
}

func TestStatuses(t *testing.T) {
	t.Parallel()

	// The wire values are stable strings.
	want := []string{"drifted", "in-sync", "unknown"}

	got := drift.Statuses()
	if len(got) != len(want) {
		t.Fatalf("Statuses() has %d values, want %d", len(got), len(want))
	}

	for i, s := range got {
		if string(s) != want[i] || !s.Valid() {
			t.Errorf("Statuses()[%d] = %q, valid %v, want %q", i, s, s.Valid(), want[i])
		}
	}

	for _, bad := range []drift.Status{"", "Drifted", "in sync", "failed"} {
		if bad.Valid() {
			t.Errorf("Status(%q).Valid() = true, want false", bad)
		}
	}
}

func TestKinds(t *testing.T) {
	t.Parallel()

	for _, k := range []drift.Kind{drift.KindChanged, drift.KindDeleted} {
		if !k.Valid() {
			t.Errorf("Kind(%q).Valid() = false, want true", k)
		}
	}

	for _, bad := range []drift.Kind{"", "created", "Changed"} {
		if bad.Valid() {
			t.Errorf("Kind(%q).Valid() = true, want false", bad)
		}
	}
}

func TestResultStatus(t *testing.T) {
	t.Parallel()

	if got := result().Status(); got != drift.StatusInSync {
		t.Errorf("a result with no resource has status %q, want in-sync", got)
	}

	if got := result(changed("aws_s3_bucket.logs")).Status(); got != drift.StatusDrifted {
		t.Errorf("a result with a resource has status %q, want drifted", got)
	}
}

func TestResultValidate(t *testing.T) {
	t.Parallel()

	many := make([]drift.Resource, drift.MaxResources+1)
	for i := range many {
		many[i] = changed(strings.Repeat("a", 8) + string(rune('A'+i%26)) + strings.Repeat("b", i/26))
	}

	valid := func(mutate func(*drift.Result)) drift.Result {
		r := result(
			changed("aws_s3_bucket.logs"),
			drift.Resource{Address: "aws_instance.web[0]", Kind: drift.KindDeleted},
		)
		mutate(&r)

		return r
	}

	withAddress := func(address string) drift.Result {
		return valid(func(r *drift.Result) { r.Resources = []drift.Resource{changed(address)} })
	}

	// Each case sends hostile input directly to Validate, as if no other
	// layer had checked it (defense in depth).
	tests := []struct {
		name   string
		result drift.Result
		ok     bool
	}{
		{"valid", valid(func(*drift.Result) {}), true},
		{"valid with no resource", result(), true},
		{"valid truncated", valid(func(r *drift.Result) {
			r.Truncated = true
			r.Resources = many[:drift.MaxResources]
		}), true},
		{"unit with no namespace", valid(func(r *drift.Result) { r.Unit.Namespace = "" }), false},
		{"unit with no tool", valid(func(r *drift.Result) { r.Unit.Tool = "" }), false},
		{"unit with no ID", valid(func(r *drift.Result) { r.Unit.ID = "" }), false},
		{"no job ID", valid(func(r *drift.Result) { r.JobID = "" }), false},
		{"job ID with no prefix", valid(func(r *drift.Result) { r.JobID = "1" }), false},
		{"job ID with only the prefix", valid(func(r *drift.Result) { r.JobID = job.IDPrefix }), false},
		{
			"job ID too long",
			valid(func(r *drift.Result) { r.JobID = job.ID(job.IDPrefix + strings.Repeat("a", 100)) }),
			false,
		},
		{"zero time", valid(func(r *drift.Result) { r.CheckedAt = time.Time{} }), false},
		{"truncated with no resource", func() drift.Result { r := result(); r.Truncated = true; return r }(), false},
		{"truncated below the limit", valid(func(r *drift.Result) { r.Truncated = true }), false},
		{"too many resources", valid(func(r *drift.Result) { r.Resources = many }), false},
		{"empty address", valid(func(r *drift.Result) { r.Resources = []drift.Resource{changed("")} }), false},
		{"long address", valid(func(r *drift.Result) {
			r.Resources = []drift.Resource{changed(strings.Repeat("a", drift.MaxAddressBytes+1))}
		}), false},
		{"longest address", valid(func(r *drift.Result) {
			r.Resources = []drift.Resource{changed(strings.Repeat("a", drift.MaxAddressBytes))}
		}), true},
		{"invalid UTF-8", valid(func(r *drift.Result) { r.Resources = []drift.Resource{changed("a\xffb")} }), false},
		{"newline in address", valid(func(r *drift.Result) { r.Resources = []drift.Resource{changed("a\nb")} }), false},
		{
			"carriage return in address",
			valid(func(r *drift.Result) { r.Resources = []drift.Resource{changed("a\rb")} }),
			false,
		},
		{"NUL in address", valid(func(r *drift.Result) { r.Resources = []drift.Resource{changed("a\x00b")} }), false},
		{"line separator at the start of an address", withAddress("\u2028a"), false},
		{"line separator in an address", withAddress("a\u2028b"), false},
		{"line separator at the end of an address", withAddress("a\u2028"), false},
		{"paragraph separator at the start of an address", withAddress("\u2029a"), false},
		{"paragraph separator in an address", withAddress("a\u2029b"), false},
		{"paragraph separator at the end of an address", withAddress("a\u2029"), false},
		{"next line in an address", withAddress("a\u0085b"), false},
		{"vertical tab in an address", withAddress("a\vb"), false},
		{"form feed in an address", withAddress("a\fb"), false},
		{
			"bidirectional override",
			valid(func(r *drift.Result) { r.Resources = []drift.Resource{changed("a\u202eb")} }),
			false,
		},
		{
			"zero-width space",
			valid(func(r *drift.Result) { r.Resources = []drift.Resource{changed("a\u200bb")} }),
			false,
		},
		{
			"unknown kind",
			valid(func(r *drift.Result) { r.Resources = []drift.Resource{{Address: "a", Kind: "created"}} }),
			false,
		},
		{"no kind", valid(func(r *drift.Result) { r.Resources = []drift.Resource{{Address: "a", Kind: ""}} }), false},
		{
			"address twice",
			valid(func(r *drift.Result) { r.Resources = []drift.Resource{changed("a"), changed("a")} }),
			false,
		},
		{
			"markup in address is data",
			valid(func(r *drift.Result) { r.Resources = []drift.Resource{changed("<script>x</script>")} }),
			true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := tc.result.Validate()

			if tc.ok {
				mustValid(t, err)
			} else {
				mustInvalid(t, err)
			}
		})
	}
}

func TestCheckRequestValidate(t *testing.T) {
	t.Parallel()

	unit, err := identity.UnitObject("opentofu", "acme", "network")
	if err != nil {
		t.Fatalf("unit object: %v", err)
	}

	withReason := func(reason string) drift.CheckRequest {
		return drift.CheckRequest{
			Unit: unit, Location: job.Location{Connection: "", Repository: "", Ref: "", Directory: "", Revision: ""},
			Pool: "", Reason: reason, Channel: "", SourceIP: "", UserAgent: "", RequestID: "",
		}
	}

	noUnit := withReason("")
	noUnit.Unit = identity.Object{}

	tests := []struct {
		name string
		req  drift.CheckRequest
		ok   bool
	}{
		{"valid", withReason(""), true},
		{"valid with a reason", withReason("after the incident"), true},
		{"no unit", noUnit, false},
		{"line separator at the start of a reason", withReason("\u2028a"), false},
		{"line separator in a reason", withReason("a\u2028b"), false},
		{"line separator at the end of a reason", withReason("a\u2028"), false},
		{"paragraph separator at the start of a reason", withReason("\u2029a"), false},
		{"paragraph separator in a reason", withReason("a\u2029b"), false},
		{"paragraph separator at the end of a reason", withReason("a\u2029"), false},
		{"next line in a reason", withReason("a\u0085b"), false},
		{"vertical tab in a reason", withReason("a\vb"), false},
		{"form feed in a reason", withReason("a\fb"), false},
		{"control character in a reason", withReason("a\x00b"), false},
		{"format character in a reason", withReason("a\u202eb"), false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := tc.req.Validate()

			if tc.ok {
				mustValid(t, err)
			} else {
				mustInvalid(t, err)
			}
		})
	}
}

func TestLatest(t *testing.T) {
	t.Parallel()

	at := time.UnixMilli(1_800_000_000_000).UTC()
	l := drift.Latest{
		CheckedAt: at,
		Unit:      unit(),
		JobID:     "job-1",
		Status:    drift.StatusDrifted,
		Resources: []drift.Resource{changed("a"), changed("b")},
		Truncated: false,
	}

	if got := l.ResourceCount(); got != 2 {
		t.Errorf("ResourceCount() = %d, want 2", got)
	}

	if got := l.Age(at.Add(90 * time.Second)); got != 90*time.Second {
		t.Errorf("Age() = %v, want 90s", got)
	}

	if got := l.Age(at.Add(-time.Hour)); got != 0 {
		t.Errorf("Age() before the check = %v, want 0", got)
	}
}

func TestTransition(t *testing.T) {
	t.Parallel()

	tests := []struct {
		from, to                  drift.Status
		changed, drifted, cleared bool
	}{
		{drift.StatusUnknown, drift.StatusDrifted, true, true, false},
		{drift.StatusInSync, drift.StatusDrifted, true, true, false},
		{drift.StatusDrifted, drift.StatusDrifted, false, false, false},
		{drift.StatusDrifted, drift.StatusInSync, true, false, true},
		{drift.StatusDrifted, drift.StatusUnknown, true, false, false},
		{drift.StatusUnknown, drift.StatusInSync, true, false, false},
		{drift.StatusInSync, drift.StatusInSync, false, false, false},
	}

	for _, tc := range tests {
		t.Run(string(tc.from)+" to "+string(tc.to), func(t *testing.T) {
			t.Parallel()

			tr := drift.Transition{At: time.Time{}, Unit: unit(), JobID: "job-1", From: tc.from, To: tc.to}

			if got := tr.Changed(); got != tc.changed {
				t.Errorf("Changed() = %v, want %v", got, tc.changed)
			}

			if got := tr.BecameDrifted(); got != tc.drifted {
				t.Errorf("BecameDrifted() = %v, want %v", got, tc.drifted)
			}

			if got := tr.Cleared(); got != tc.cleared {
				t.Errorf("Cleared() = %v, want %v", got, tc.cleared)
			}
		})
	}
}
