package unmanagedtest

import (
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/unmanaged"
)

// findingCases returns the cases of the findings.
func findingCases() []suiteCase {
	return []suiteCase{
		{"findings: a new finding is open, and a second record changes nothing", testRecordNew},
		{"findings: a later scan observes and reclassifies", testRecordLater},
		{"findings: a managed resource resolves its finding", testRecordResolves},
		{"findings: an ignore rule ignores a new finding", testRuleApplies},
		{"findings: an action checks the status that the caller read", testActChecks},
		{"findings: unseen findings resolve only in complete scopes", testResolveUnseen},
		{"findings: ignores end at their time", testExpireIgnores},
		{"findings: a stale fence refuses each write", testFindingsFenced},
	}
}

// matchingScan starts a scan of kind k and moves it to ScanMatching. A cloud
// scan needs a target, which it makes.
func matchingScan(
	t *testing.T,
	h Harness,
	f namespace.Fence,
	k unmanaged.ScanKind,
	scopes []unmanaged.ReportScope,
) unmanaged.Scan {
	t.Helper()

	ns := f.Namespace.String()
	s := unmanaged.Scan{
		Namespace: f.Namespace,
		Kind:      k,
		CreatedAt: at(),
		EndedAt:   time.Time{},
		ID:        "",
		Target:    "",
		Status:    "",
		StartedBy: "",
		Reason:    "",
		Scopes:    nil,
		Counts: unmanaged.ScanCounts{
			Records:      0,
			Managed:      0,
			Elsewhere:    0,
			NotInAnyUnit: 0,
			LeftBehind:   0,
			ManagedTwice: 0,
			Resolved:     0,
			Rejected:     0,
		},
	}

	if k == unmanaged.ScanCloud {
		target := targetOf(ns, "aws")
		if err := h.Scans.CreateTarget(t.Context(), target, f, auditOf(ns, "namespace:manage-scans")); err != nil {
			t.Fatalf("create target: %v", err)
		}

		s.Target = target.Name
	}

	s, err := h.Scans.StartScan(t.Context(), s, f, auditOf(ns, "namespace:start-scan"))
	must(t, "start scan", err)

	steps := []unmanaged.ScanEvent{unmanaged.ScanEventUploaded}
	if k == unmanaged.ScanCloud {
		steps = []unmanaged.ScanEvent{
			unmanaged.ScanEventDispatch,
			unmanaged.ScanEventClaim,
			unmanaged.ScanEventUploaded,
		}
	}

	for _, ev := range steps {
		s, err = h.Scans.AdvanceScan(
			t.Context(),
			f.Namespace,
			s.ID,
			ev,
			unmanaged.ScanUpdate{Scopes: scopes, Counts: nil, Reason: ""},
			f,
			auditOf(ns, "namespace:start-scan"),
		)
		must(t, string(ev), err)
	}

	return s
}

func targetOf(ns, name string) unmanaged.Target {
	return unmanaged.Target{
		Namespace: namespace.Name(ns), Name: name, Pool: Pool, Credentials: unmanaged.CredentialsRunner,
		Scopes:  []unmanaged.TargetScope{{Provider: "aws", Scope: "123456789012", Regions: nil}},
		Command: []string{"inventory-scan"}, Every: 24 * time.Hour, Enabled: true,
		Profile: "",
		Timeout: 0,
	}
}

func observation(key unmanaged.ResourceKey, c unmanaged.Class, holders ...unmanaged.Holder) unmanaged.Observation {
	return unmanaged.Observation{Key: key, Class: c, Holders: holders, Record: nil}
}

// listAll returns the findings of namespace acme.
func listAll(t *testing.T, h Harness) []unmanaged.Finding {
	t.Helper()

	got, err := h.Findings.List(
		t.Context(),
		"acme",
		unmanaged.Filter{Unit: nil, Provider: "", Scope: "", Type: "", Kinds: nil, Statuses: nil},
		"",
		unmanaged.MaxPage,
	)
	must(t, "list", err)

	return got
}

// firstFinding returns the first finding of namespace acme. It fails the
// test when the namespace has no finding.
func firstFinding(t *testing.T, h Harness) unmanaged.Finding {
	t.Helper()

	var first unmanaged.Finding

	all := listAll(t, h)
	if len(all) == 0 {
		t.Fatal("no finding in acme, want one")

		return first
	}

	first = all[0]

	return first
}

func testRecordNew(t *testing.T, h Harness) {
	t.Helper()

	f := h.Namespace(t, "acme")
	s := matchingScan(t, h, f, unmanaged.ScanState, nil)
	gone := unmanaged.Holder{Unit: Unit("acme", "gone"), Resource: "aws_s3_bucket.b", Deleted: true, Type: ""}
	obs := []unmanaged.Observation{observation(AWSKey("left"), unmanaged.ClassLeftBehind, gone)}

	for range 2 {
		_, err := h.Findings.Record(t.Context(), s, obs, at(), f)
		must(t, "record", err)
	}

	got := listAll(t, h)
	if len(got) != 1 {
		t.Fatalf("findings %d, want 1", len(got))
	}

	fd := got[0]
	if fd.Kind != unmanaged.KindLeftBehind || fd.Status != unmanaged.StatusOpen ||
		fd.Evidence != unmanaged.EvidenceState ||
		len(fd.Units) != 1 ||
		fd.Units[0] != gone.Unit ||
		fd.Scan != s.ID {
		t.Errorf("finding %+v", fd)
	}
}

func testRecordLater(t *testing.T, h Harness) {
	t.Helper()

	f := h.Namespace(t, "acme")
	gone := unmanaged.Holder{Unit: Unit("acme", "gone"), Deleted: true, Resource: "", Type: ""}

	first := matchingScan(t, h, f, unmanaged.ScanState, nil)
	_, err := h.Findings.Record(t.Context(), first, []unmanaged.Observation{
		observation(AWSKey("b"), unmanaged.ClassLeftBehind, gone),
	}, at(), f)
	must(t, "record first", err)

	ended, err := h.Scans.AdvanceScan(
		t.Context(),
		"acme",
		first.ID,
		unmanaged.ScanEventMatched,
		unmanaged.ScanUpdate{Scopes: nil, Counts: nil, Reason: ""},
		f,
		auditOf("acme", "namespace:start-scan"),
	)
	must(t, "end first", err)

	if !ended.Status.Terminal() {
		t.Fatalf("scan %s, want an end status", ended.Status)
	}

	second := matchingScan(
		t,
		h,
		f,
		unmanaged.ScanCloud,
		[]unmanaged.ReportScope{{Provider: "aws", Scope: "123456789012", Complete: true, Error: "", Regions: nil}},
	)
	rec := &unmanaged.Record{
		Provider: "aws",
		ID:       AWSKey("b").ID,
		Type:     "AWS::S3::Bucket",
		Tags:     nil,
		Region:   "",
		Name:     "",
	}
	obs := observation(AWSKey("b"), unmanaged.ClassNotInAnyUnit)
	obs.Record = rec

	_, err = h.Findings.Record(t.Context(), second, []unmanaged.Observation{obs}, at().Add(time.Hour), f)
	must(t, "record second", err)

	got := listAll(t, h)
	if len(got) != 1 || got[0].Kind != unmanaged.KindNotInAnyUnit || got[0].Evidence != unmanaged.EvidenceScan ||
		!got[0].LastSeen.Equal(at().Add(time.Hour)) || !got[0].FirstSeen.Equal(at()) {
		t.Errorf("findings %+v, want one finding reclassified as not in any unit", got)
	}
}

func testRecordResolves(t *testing.T, h Harness) {
	t.Helper()

	f := h.Namespace(t, "acme")
	s := matchingScan(t, h, f, unmanaged.ScanState, nil)
	a, b := unmanaged.Holder{
		Unit:     Unit("acme", "a"),
		Resource: "",
		Type:     "",
		Deleted:  false,
	}, unmanaged.Holder{
		Unit:     Unit("acme", "b"),
		Resource: "",
		Type:     "",
		Deleted:  false,
	}

	_, err := h.Findings.Record(
		t.Context(),
		s,
		[]unmanaged.Observation{observation(AWSKey("x"), unmanaged.ClassManagedTwice, a, b)},
		at(),
		f,
	)
	must(t, "record twice", err)

	_, err = h.Scans.AdvanceScan(
		t.Context(),
		"acme",
		s.ID,
		unmanaged.ScanEventMatched,
		unmanaged.ScanUpdate{Scopes: nil, Counts: nil, Reason: ""},
		f,
		auditOf("acme", "namespace:start-scan"),
	)
	must(t, "end first", err)

	// The same key in the same scan again changes nothing, so the second
	// observation comes from a second scan.
	next := matchingScan(t, h, f, unmanaged.ScanState, nil)

	counts, err := h.Findings.Record(
		t.Context(),
		next,
		[]unmanaged.Observation{observation(AWSKey("x"), unmanaged.ClassManaged, a)},
		at(),
		f,
	)
	must(t, "record managed", err)

	got := listAll(t, h)
	if len(got) != 1 || got[0].Status != unmanaged.StatusResolved || counts.Resolved != 1 {
		t.Errorf("findings %+v counts %+v, want one resolved finding", got, counts)
	}
}

func testRuleApplies(t *testing.T, h Harness) {
	t.Helper()

	f := h.Namespace(t, "acme")
	rule := unmanaged.IgnoreRule{
		ID: "default-vpc", Namespace: "acme", Provider: "aws", Type: "AWS::EC2::*",
		Reason: "Default network objects of each account.", By: "user:ada",
		Until:    time.Time{},
		Scope:    "",
		TagKey:   "",
		TagValue: "",
		Kinds:    nil,
	}

	must(t, "put rule", h.Findings.PutRule(t.Context(), rule, at(), f, auditOf("acme", "namespace:triage-unmanaged")))
	wantErr(
		t,
		"put the rule again",
		h.Findings.PutRule(
			t.Context(),
			rule,
			at(),
			f,
			auditOf("acme", "namespace:triage-unmanaged"),
		),
		unmanaged.ErrAlreadyExists,
	)

	s := matchingScan(
		t,
		h,
		f,
		unmanaged.ScanCloud,
		[]unmanaged.ReportScope{{Provider: "aws", Scope: "123456789012", Complete: true, Error: "", Regions: nil}},
	)
	key := unmanaged.ResourceKey{Provider: "aws", ID: "arn:aws:ec2:eu-west-1:123456789012:vpc/vpc-1"}
	obs := observation(key, unmanaged.ClassNotInAnyUnit)
	obs.Record = &unmanaged.Record{Provider: "aws", ID: key.ID, Type: "AWS::EC2::VPC", Tags: nil, Region: "", Name: ""}

	_, err := h.Findings.Record(t.Context(), s, []unmanaged.Observation{obs}, at(), f)
	must(t, "record", err)

	got := listAll(t, h)
	if len(got) != 1 || got[0].Status != unmanaged.StatusIgnored || got[0].Ignore == nil ||
		got[0].Ignore.Rule != rule.ID {
		t.Errorf("findings %+v, want one finding ignored by the rule", got)
	}
}

func testActChecks(t *testing.T, h Harness) {
	t.Helper()

	f := h.Namespace(t, "acme")
	s := matchingScan(t, h, f, unmanaged.ScanState, nil)

	_, err := h.Findings.Record(t.Context(), s, []unmanaged.Observation{
		observation(
			AWSKey("x"),
			unmanaged.ClassLeftBehind,
			unmanaged.Holder{Unit: Unit("acme", "gone"), Deleted: true, Resource: "", Type: ""},
		),
	}, at(), f)
	must(t, "record", err)

	id := firstFinding(t, h).ID
	e := auditOf("acme", "namespace:triage-unmanaged")
	ignore := &unmanaged.Ignore{Reason: "Kept for the audit.", By: "user:ada", Until: time.Time{}, Rule: ""}

	_, err = h.Findings.Act(t.Context(), "acme", id, unmanaged.StatusPending, unmanaged.EventIgnore,
		unmanaged.Change{Ignore: ignore, Pending: nil}, f, e)
	wantErr(t, "act from a stale status", err, unmanaged.ErrStatusChanged)

	_, err = h.Findings.Act(
		t.Context(),
		"acme",
		id,
		unmanaged.StatusOpen,
		unmanaged.EventUnignore,
		unmanaged.Change{Ignore: nil, Pending: nil},
		f,
		e,
	)
	wantErr(t, "act with an event the table refuses", err, unmanaged.ErrTransition)

	_, err = h.Findings.Act(
		t.Context(),
		"acme",
		id,
		unmanaged.StatusOpen,
		unmanaged.EventIgnore,
		unmanaged.Change{
			Ignore:  &unmanaged.Ignore{By: "user:ada", Until: time.Time{}, Reason: "", Rule: ""},
			Pending: nil,
		},
		f,
		e,
	)
	wantErr(t, "ignore with no reason", err, unmanaged.ErrInvalidReason)

	got, err := h.Findings.Act(t.Context(), "acme", id, unmanaged.StatusOpen, unmanaged.EventIgnore,
		unmanaged.Change{Ignore: ignore, Pending: nil}, f, e)
	must(t, "ignore", err)

	if got.Status != unmanaged.StatusIgnored || got.Ignore == nil || got.Ignore.Reason != ignore.Reason {
		t.Errorf("finding %+v, want ignored with the reason", got)
	}

	got, err = h.Findings.Act(
		t.Context(),
		"acme",
		id,
		unmanaged.StatusIgnored,
		unmanaged.EventUnignore,
		unmanaged.Change{Ignore: nil, Pending: nil},
		f,
		e,
	)
	must(t, "unignore", err)

	if got.Status != unmanaged.StatusOpen || got.Ignore != nil {
		t.Errorf("finding %+v, want open with no ignore", got)
	}

	_, err = h.Findings.Act(t.Context(), "acme", "fnd-none", unmanaged.StatusOpen, unmanaged.EventIgnore,
		unmanaged.Change{Ignore: ignore, Pending: nil}, f, e)
	wantErr(t, "act on a missing finding", err, unmanaged.ErrNotFound)
}

func testResolveUnseen(t *testing.T, h Harness) {
	t.Helper()

	f := h.Namespace(t, "acme")
	seen := func(s unmanaged.Scan, keys ...unmanaged.ResourceKey) {
		obs := make([]unmanaged.Observation, 0, len(keys))
		for _, k := range keys {
			o := observation(k, unmanaged.ClassNotInAnyUnit)
			o.Record = &unmanaged.Record{Provider: k.Provider, ID: k.ID, Type: "t", Tags: nil, Region: "", Name: ""}
			obs = append(obs, o)
		}

		_, err := h.Findings.Record(t.Context(), s, obs, at(), f)
		must(t, "record", err)
	}

	inScope := unmanaged.ResourceKey{Provider: "aws", ID: "arn:aws:ec2:eu-west-1:111111111111:vpc/vpc-1"}
	outScope := unmanaged.ResourceKey{Provider: "aws", ID: "arn:aws:ec2:eu-west-1:222222222222:vpc/vpc-2"}

	first := matchingScan(t, h, f, unmanaged.ScanCloud, []unmanaged.ReportScope{
		{
			Provider: "aws",
			Scope:    "111111111111",
			Complete: true,
			Error:    "",
			Regions:  nil,
		},
		{Provider: "aws", Scope: "222222222222", Complete: true, Error: "", Regions: nil},
	})
	seen(first, inScope, outScope)

	_, err := h.Scans.AdvanceScan(
		t.Context(),
		"acme",
		first.ID,
		unmanaged.ScanEventMatched,
		unmanaged.ScanUpdate{Scopes: nil, Counts: nil, Reason: ""},
		f,
		auditOf("acme", "namespace:start-scan"),
	)
	must(t, "end first", err)

	// The second scan sees neither key. Only the account 111111111111 is
	// complete, so only the finding of that account resolves.
	second := unmanaged.Scan{
		Namespace: "acme",
		Kind:      unmanaged.ScanCloud,
		Target:    first.Target,
		CreatedAt: at(),
		EndedAt:   time.Time{},
		ID:        "",
		Status:    "",
		StartedBy: "",
		Reason:    "",
		Scopes:    nil,
		Counts: unmanaged.ScanCounts{
			Records:      0,
			Managed:      0,
			Elsewhere:    0,
			NotInAnyUnit: 0,
			LeftBehind:   0,
			ManagedTwice: 0,
			Resolved:     0,
			Rejected:     0,
		},
	}
	second, err = h.Scans.StartScan(t.Context(), second, f, auditOf("acme", "namespace:start-scan"))
	must(t, "start second", err)

	for _, ev := range []unmanaged.ScanEvent{unmanaged.ScanEventDispatch, unmanaged.ScanEventClaim, unmanaged.ScanEventUploaded} {
		second, err = h.Scans.AdvanceScan(
			t.Context(),
			"acme",
			second.ID,
			ev,
			unmanaged.ScanUpdate{Scopes: []unmanaged.ReportScope{
				{Provider: "aws", Scope: "111111111111", Complete: true, Error: "", Regions: nil},
				{Provider: "aws", Scope: "222222222222", Error: "access denied", Regions: nil, Complete: false},
			}, Counts: nil, Reason: ""},
			f,
			auditOf("acme", "namespace:start-scan"),
		)
		must(t, string(ev), err)
	}

	n, err := h.Findings.ResolveUnseen(t.Context(), second, at(), f)
	must(t, "resolve unseen", err)

	if n != 1 {
		t.Errorf("resolved %d, want 1", n)
	}

	all := listAll(t, h)
	for i := range all {
		fd := &all[i]

		want := unmanaged.StatusOpen
		if fd.Key == inScope {
			want = unmanaged.StatusResolved
		}

		if fd.Status != want {
			t.Errorf("finding %s is %s, want %s", fd.Key.ID, fd.Status, want)
		}
	}
}

func testExpireIgnores(t *testing.T, h Harness) {
	t.Helper()

	f := h.Namespace(t, "acme")
	s := matchingScan(t, h, f, unmanaged.ScanState, nil)

	_, err := h.Findings.Record(t.Context(), s, []unmanaged.Observation{
		observation(
			AWSKey("x"),
			unmanaged.ClassLeftBehind,
			unmanaged.Holder{Unit: Unit("acme", "gone"), Deleted: true, Resource: "", Type: ""},
		),
	}, at(), f)
	must(t, "record", err)

	id := firstFinding(t, h).ID
	_, err = h.Findings.Act(
		t.Context(),
		"acme",
		id,
		unmanaged.StatusOpen,
		unmanaged.EventIgnore,
		unmanaged.Change{
			Ignore: &unmanaged.Ignore{
				Reason: "Until the move.",
				By:     "user:ada",
				Until:  at().Add(24 * time.Hour),
				Rule:   "",
			},
			Pending: nil,
		},
		f,
		auditOf("acme", "namespace:triage-unmanaged"),
	)
	must(t, "ignore", err)

	n, err := h.Findings.ExpireIgnores(t.Context(), "acme", at().Add(time.Hour), f)
	must(t, "expire early", err)

	if n != 0 {
		t.Errorf("expired %d before the end, want 0", n)
	}

	n, err = h.Findings.ExpireIgnores(t.Context(), "acme", at().Add(24*time.Hour), f)
	must(t, "expire", err)

	if got := listAll(t, h); n != 1 || len(got) == 0 || got[0].Status != unmanaged.StatusOpen {
		t.Errorf("expired %d, finding %+v; want 1 and open", n, got)
	}
}

func testFindingsFenced(t *testing.T, h Harness) {
	t.Helper()

	f := h.Namespace(t, "acme")
	s := matchingScan(t, h, f, unmanaged.ScanState, nil)
	old := stale(f)

	_, err := h.Findings.Record(t.Context(), s, nil, at(), old)
	wantErr(t, "record", err, namespace.ErrFenced)

	_, err = h.Findings.ResolveUnseen(t.Context(), s, at(), old)
	wantErr(t, "resolve unseen", err, namespace.ErrFenced)

	_, err = h.Findings.ExpireIgnores(t.Context(), "acme", at(), old)
	wantErr(t, "expire", err, namespace.ErrFenced)

	rule := unmanaged.IgnoreRule{
		ID:        "r",
		Namespace: "acme",
		Provider:  "aws",
		Reason:    "r",
		Until:     time.Time{},
		Scope:     "",
		Type:      "",
		TagKey:    "",
		TagValue:  "",
		By:        "",
		Kinds:     nil,
	}
	wantErr(t, "put rule", h.Findings.PutRule(t.Context(), rule, at(), old, auditOf("acme", "x")), namespace.ErrFenced)
}
