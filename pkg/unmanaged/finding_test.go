package unmanaged_test

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/unmanaged"
)

//nolint:gochecknoglobals // A fixed test time that several tests read.
var now = time.UnixMilli(1_800_000_000_000).UTC()

// TestNext checks every pair of status and event against this table, which
// is written out apart from the code. A pair that is not in the table fails
// with ErrTransition.
func TestNext(t *testing.T) {
	t.Parallel()

	const (
		open     = unmanaged.StatusOpen
		ignored  = unmanaged.StatusIgnored
		pending  = unmanaged.StatusPending
		resolved = unmanaged.StatusResolved
	)

	want := map[unmanaged.Status]map[unmanaged.Event]unmanaged.Status{
		open: {
			"observe": open, "reclassify": open, "ignore": ignored, "act": pending, "resolve": resolved,
		},
		ignored: {
			"observe": ignored, "reclassify": open, "unignore": open, "ignore-expired": open, "resolve": resolved,
		},
		pending: {
			"observe": pending, "reclassify": open, "action-ended": open, "resolve": resolved, "cancel": open,
			"pending-expired": open,
		},
		resolved: {
			"observe": open, "reclassify": open, "resolve": resolved,
		},
	}

	events := []unmanaged.Event{
		unmanaged.EventObserve, unmanaged.EventReclassify, unmanaged.EventIgnore, unmanaged.EventUnignore,
		unmanaged.EventIgnoreExpired, unmanaged.EventAct, unmanaged.EventActionEnded, unmanaged.EventResolve,
		unmanaged.EventCancel, unmanaged.EventPendingExpired,
	}

	for from, row := range want {
		for _, ev := range events {
			got, err := unmanaged.Next(from, ev)

			to, ok := row[ev]
			switch {
			case ok && (err != nil || got != to):
				t.Errorf("%s + %s = %s, %v; want %s", from, ev, got, err, to)
			case !ok && !errors.Is(err, unmanaged.ErrTransition):
				t.Errorf("%s + %s = %s, %v; want ErrTransition", from, ev, got, err)
			default:
				// The transition is as the table says.
			}
		}
	}
}

// scanStep is one event on a sweep of one kind in one status.
type scanStep struct {
	kind unmanaged.ScanKind
	from unmanaged.ScanStatus
	ev   unmanaged.ScanEvent
}

// scanTable returns the state table of a sweep, written out apart from the
// code. A step that is not in the table fails with ErrTransition.
func scanTable() map[scanStep]unmanaged.ScanStatus {
	type key = scanStep

	want := map[key]unmanaged.ScanStatus{}

	for _, k := range []unmanaged.ScanKind{unmanaged.ScanCloud, unmanaged.ScanState} {
		want[key{k, "queued", "cancel"}] = "canceled"
		want[key{k, "queued", "expire"}] = "expired"
		want[key{k, "matching", "matched"}] = "succeeded"
		want[key{k, "matching", "fail"}] = "failed"
	}

	want[key{unmanaged.ScanState, "queued", "uploaded"}] = "matching"

	for from, row := range map[unmanaged.ScanStatus]map[unmanaged.ScanEvent]unmanaged.ScanStatus{
		"queued":     {"dispatch": "dispatched"},
		"dispatched": {"claim": "running", "cancel": "canceled", "expire": "expired"},
		"running": {
			"claim": "running", "release": "dispatched", "uploaded": "matching", "fail": "failed",
			"cancel": "canceled", "expire": "expired",
		},
	} {
		for ev, to := range row {
			want[key{unmanaged.ScanCloud, from, ev}] = to
		}
	}

	return want
}

// TestNextScan checks every pair of status and event of a sweep of each kind
// against the table of scanTable.
func TestNextScan(t *testing.T) {
	t.Parallel()

	want := scanTable()

	statuses := []unmanaged.ScanStatus{
		"queued", "dispatched", "running", "matching", "succeeded", "failed", "canceled", "expired",
	}
	events := []unmanaged.ScanEvent{
		"dispatch", "claim", "release", "uploaded", "matched", "fail", "cancel", "expire",
	}

	for _, k := range []unmanaged.ScanKind{unmanaged.ScanCloud, unmanaged.ScanState} {
		for _, from := range statuses {
			for _, ev := range events {
				got, err := unmanaged.NextScan(k, from, ev)

				to, ok := want[scanStep{k, from, ev}]
				switch {
				case ok && (err != nil || got != to):
					t.Errorf("%s %s + %s = %s, %v; want %s", k, from, ev, got, err, to)
				case !ok && !errors.Is(err, unmanaged.ErrTransition):
					t.Errorf("%s %s + %s = %s, %v; want ErrTransition", k, from, ev, got, err)
				default:
					// The transition is as the table says.
				}
			}
		}
	}
}

func TestPendingWait(t *testing.T) {
	t.Parallel()

	const day = 24 * time.Hour

	cases := []struct {
		every, want time.Duration
	}{
		{0, unmanaged.MinPending},
		{unmanaged.MinEvery, unmanaged.MinPending},
		{7 * day, unmanaged.MinPending},
		{10 * day, 20 * day},
		{unmanaged.MaxEvery, 2 * unmanaged.MaxEvery},
	}

	for _, tc := range cases {
		if got := unmanaged.PendingWait(tc.every); got != tc.want {
			t.Errorf("PendingWait(%v) = %v, want %v", tc.every, got, tc.want)
		}
	}
}

func TestSpecOf(t *testing.T) {
	t.Parallel()

	cases := []struct {
		action unmanaged.ActionKind
		kind   unmanaged.Kind
		ok     bool
	}{
		{unmanaged.ActionImport, unmanaged.KindNotInAnyUnit, true},
		{unmanaged.ActionImport, unmanaged.KindLeftBehind, true},
		{unmanaged.ActionImport, unmanaged.KindManagedTwice, false},
		{unmanaged.ActionWriteCode, unmanaged.KindNotInAnyUnit, true},
		{unmanaged.ActionDelete, unmanaged.KindLeftBehind, true},
		{unmanaged.ActionDelete, unmanaged.KindManagedTwice, false},
		{unmanaged.ActionChooseOwner, unmanaged.KindManagedTwice, true},
		{unmanaged.ActionChooseOwner, unmanaged.KindNotInAnyUnit, false},
		{unmanaged.ActionIgnore, unmanaged.KindManagedTwice, true},
		{unmanaged.ActionCancel, unmanaged.KindLeftBehind, true},
		{"adopt", unmanaged.KindNotInAnyUnit, false},
	}

	for _, tc := range cases {
		_, err := unmanaged.SpecOf(tc.action, tc.kind)
		if (err == nil) != tc.ok || (err != nil && !errors.Is(err, unmanaged.ErrActionKind)) {
			t.Errorf("SpecOf(%s, %s) = %v, want ok %v", tc.action, tc.kind, err, tc.ok)
		}
	}

	for _, s := range unmanaged.ActionCatalog() {
		if s.StartsJob && !containsAction(s.UnitActions, identity.ActionUnitWrite) {
			t.Errorf("action %s starts a job and does not check unit:write on its unit", s.Action)
		}
	}
}

func containsAction(as []identity.Action, a identity.Action) bool {
	return slices.Contains(as, a)
}

func TestCheckIgnore(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   unmanaged.Ignore
		ok   bool
	}{
		{
			"no end",
			unmanaged.Ignore{Reason: "The default VPC of the account.", Until: time.Time{}, By: "", Rule: ""},
			true,
		},
		{
			"end in 30 days",
			unmanaged.Ignore{Reason: "Migration in progress.", Until: now.Add(30 * 24 * time.Hour), By: "", Rule: ""},
			true,
		},
		{"end in the past", unmanaged.Ignore{Reason: "x", Until: now.Add(-time.Hour), By: "", Rule: ""}, false},
		{
			"end too far",
			unmanaged.Ignore{Reason: "x", Until: now.Add(unmanaged.MaxIgnore + time.Hour), By: "", Rule: ""},
			false,
		},
		{"empty reason", unmanaged.Ignore{Reason: " ", Until: time.Time{}, By: "", Rule: ""}, false},
		{
			"long reason",
			unmanaged.Ignore{
				Reason: strings.Repeat("a", unmanaged.MaxReasonBytes+1),
				Until:  time.Time{},
				By:     "",
				Rule:   "",
			},
			false,
		},
		{"reason with a line feed", unmanaged.Ignore{Reason: "a\nb", Until: time.Time{}, By: "", Rule: ""}, false},
	}

	for _, tc := range cases {
		err := unmanaged.CheckIgnore(tc.in, now)
		if (err == nil) != tc.ok || (err != nil && !errors.Is(err, unmanaged.ErrInvalidReason)) {
			t.Errorf("%s: CheckIgnore = %v, want ok %v", tc.name, err, tc.ok)
		}
	}
}

func TestIgnoreRule(t *testing.T) {
	t.Parallel()

	f := unmanaged.Finding{
		Kind: unmanaged.KindNotInAnyUnit, Type: "AWS::EC2::VPC",
		Key:       unmanaged.ResourceKey{Provider: "aws", ID: "arn:aws:ec2:eu-west-1:123456789012:vpc/vpc-1"},
		Tags:      map[string]string{"aws:default": "true"},
		FirstSeen: time.Time{},
		LastSeen:  time.Time{},
		Ignore:    nil,
		Pending:   nil,
		ID:        "",
		Namespace: "",
		Status:    "",
		Evidence:  "",
		Scope:     "123456789012",
		Region:    "",
		Name:      "",
		Scan:      "",
		Units:     nil,
		Elsewhere: false,
	}

	cases := []struct {
		name    string
		rule    unmanaged.IgnoreRule
		valid   bool
		matches bool
	}{
		{
			"provider only",
			unmanaged.IgnoreRule{
				Provider:  "aws",
				Reason:    "r",
				Until:     time.Time{},
				ID:        "",
				Namespace: "",
				Scope:     "",
				Type:      "",
				TagKey:    "",
				TagValue:  "",
				By:        "",
				Kinds:     nil,
			},
			true,
			true,
		},
		{
			"type prefix",
			unmanaged.IgnoreRule{
				Provider:  "aws",
				Type:      "AWS::EC2::*",
				Reason:    "r",
				Until:     time.Time{},
				ID:        "",
				Namespace: "",
				Scope:     "",
				TagKey:    "",
				TagValue:  "",
				By:        "",
				Kinds:     nil,
			},
			true,
			true,
		},
		{
			"other type",
			unmanaged.IgnoreRule{
				Provider:  "aws",
				Type:      "AWS::S3::Bucket",
				Reason:    "r",
				Until:     time.Time{},
				ID:        "",
				Namespace: "",
				Scope:     "",
				TagKey:    "",
				TagValue:  "",
				By:        "",
				Kinds:     nil,
			},
			true,
			false,
		},
		{
			"tag key and value",
			unmanaged.IgnoreRule{
				Provider:  "aws",
				TagKey:    "aws:default",
				TagValue:  "true",
				Reason:    "r",
				Until:     time.Time{},
				ID:        "",
				Namespace: "",
				Scope:     "",
				Type:      "",
				By:        "",
				Kinds:     nil,
			},
			true,
			true,
		},
		{
			"tag key with another value",
			unmanaged.IgnoreRule{
				Provider:  "aws",
				TagKey:    "aws:default",
				TagValue:  "false",
				Reason:    "r",
				Until:     time.Time{},
				ID:        "",
				Namespace: "",
				Scope:     "",
				Type:      "",
				By:        "",
				Kinds:     nil,
			},
			true,
			false,
		},
		{
			"scope",
			unmanaged.IgnoreRule{
				Provider:  "aws",
				Scope:     "123456789012",
				Reason:    "r",
				Until:     time.Time{},
				ID:        "",
				Namespace: "",
				Type:      "",
				TagKey:    "",
				TagValue:  "",
				By:        "",
				Kinds:     nil,
			},
			true,
			true,
		},
		{
			"other scope",
			unmanaged.IgnoreRule{
				Provider:  "aws",
				Scope:     "999",
				Reason:    "r",
				Until:     time.Time{},
				ID:        "",
				Namespace: "",
				Type:      "",
				TagKey:    "",
				TagValue:  "",
				By:        "",
				Kinds:     nil,
			},
			true,
			false,
		},
		{
			"other kind",
			unmanaged.IgnoreRule{
				Provider:  "aws",
				Kinds:     []unmanaged.Kind{unmanaged.KindManagedTwice},
				Reason:    "r",
				Until:     time.Time{},
				ID:        "",
				Namespace: "",
				Scope:     "",
				Type:      "",
				TagKey:    "",
				TagValue:  "",
				By:        "",
			},
			true,
			false,
		},
		{
			"ended",
			unmanaged.IgnoreRule{
				Provider:  "aws",
				Reason:    "r",
				Until:     now.Add(-time.Hour),
				ID:        "",
				Namespace: "",
				Scope:     "",
				Type:      "",
				TagKey:    "",
				TagValue:  "",
				By:        "",
				Kinds:     nil,
			},
			false,
			false,
		},
		{
			"no provider",
			unmanaged.IgnoreRule{
				Reason:    "r",
				Until:     time.Time{},
				ID:        "",
				Namespace: "",
				Provider:  "",
				Scope:     "",
				Type:      "",
				TagKey:    "",
				TagValue:  "",
				By:        "",
				Kinds:     nil,
			},
			false,
			false,
		},
		{
			"star inside the type",
			unmanaged.IgnoreRule{
				Provider:  "aws",
				Type:      "AWS::*::VPC",
				Reason:    "r",
				Until:     time.Time{},
				ID:        "",
				Namespace: "",
				Scope:     "",
				TagKey:    "",
				TagValue:  "",
				By:        "",
				Kinds:     nil,
			},
			false,
			false,
		},
		{
			"value with no key",
			unmanaged.IgnoreRule{
				Provider:  "aws",
				TagValue:  "x",
				Reason:    "r",
				Until:     time.Time{},
				ID:        "",
				Namespace: "",
				Scope:     "",
				Type:      "",
				TagKey:    "",
				By:        "",
				Kinds:     nil,
			},
			false,
			true,
		},
	}

	for _, tc := range cases {
		if err := tc.rule.Validate(now); (err == nil) != tc.valid {
			t.Errorf("%s: Validate = %v, want valid %v", tc.name, err, tc.valid)
		}

		if got := tc.rule.Matches(f, now); got != tc.matches {
			t.Errorf("%s: Matches = %v, want %v", tc.name, got, tc.matches)
		}
	}
}

func TestTargetValidate(t *testing.T) {
	t.Parallel()

	good := unmanaged.Target{
		Namespace: "acme", Name: "aws-prod", Pool: "sweepers",
		Scopes:  []unmanaged.TargetScope{{Provider: "aws", Scope: "123456789012", Regions: []string{"eu-west-1"}}},
		Command: []string{"inventory-sweep", "--format", "opendustpan"}, Every: 24 * time.Hour,
		Timeout: 0,
		Enabled: false,
	}

	cases := []struct {
		name   string
		change func(*unmanaged.Target)
		ok     bool
	}{
		{"valid", func(*unmanaged.Target) {}, true},
		{"on request only", func(t *unmanaged.Target) { t.Every = 0 }, true},
		{"absolute program", func(t *unmanaged.Target) { t.Command = []string{"/usr/local/bin/sweep"} }, true},
		{"relative program", func(t *unmanaged.Target) { t.Command = []string{"./sweep"} }, false},
		{"program path not clean", func(t *unmanaged.Target) { t.Command = []string{"/usr/bin/../bin/sweep"} }, false},
		{"no command", func(t *unmanaged.Target) { t.Command = nil }, false},
		{"NUL byte", func(t *unmanaged.Target) { t.Command = []string{"sweep", "a\x00b"} }, false},
		{"schedule too often", func(t *unmanaged.Target) { t.Every = time.Minute }, false},
		{"no pool", func(t *unmanaged.Target) { t.Pool = "" }, false},
		{"a password flag", func(t *unmanaged.Target) { t.Command = []string{"sweep", "--password", "x"} }, false},
		{
			"a token flag with a value",
			func(t *unmanaged.Target) { t.Command = []string{"sweep", "--api-key=x"} },
			false,
		},
		{
			"a credential assignment",
			func(t *unmanaged.Target) { t.Command = []string{"env", "AWS_SECRET_ACCESS_KEY=x", "sweep"} },
			false,
		},
		{
			"a suffix of the rule",
			func(t *unmanaged.Target) { t.Command = []string{"sweep", "--github-token", "x"} },
			false,
		},
		{"a plain flag", func(t *unmanaged.Target) { t.Command = []string{"sweep", "--region=eu-west-1"} }, true},
		{"a path argument", func(t *unmanaged.Target) { t.Command = []string{"sweep", "/etc/sweep/token"} }, true},
		{"a scope twice", func(t *unmanaged.Target) { t.Scopes = append(t.Scopes, t.Scopes[0]) }, false},
		{"an empty region", func(t *unmanaged.Target) {
			t.Scopes = []unmanaged.TargetScope{{Provider: "aws", Scope: "1", Regions: []string{""}}}
		}, false},
		{"no scope", func(t *unmanaged.Target) { t.Scopes = nil }, false},
		{"timeout too long", func(t *unmanaged.Target) { t.Timeout = 25 * time.Hour }, false},
	}

	for _, tc := range cases {
		target := good
		target.Command = append([]string(nil), good.Command...)
		target.Scopes = append([]unmanaged.TargetScope(nil), good.Scopes...)
		tc.change(&target)

		err := target.Validate()
		if (err == nil) != tc.ok || (err != nil && !errors.Is(err, unmanaged.ErrInvalidTarget)) {
			t.Errorf("%s: Validate = %v, want ok %v", tc.name, err, tc.ok)
		}
	}
}
