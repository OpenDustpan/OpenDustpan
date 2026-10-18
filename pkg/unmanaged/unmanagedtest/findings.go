package unmanagedtest

import (
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/unmanaged"
)

// findingCases returns the cases of the findings.
func findingCases() []suiteCase {
	return []suiteCase{
		{"findings: a new finding is open, and a second record changes nothing", testRecordNew},
		{"findings: a later sweep observes and reclassifies", testRecordLater},
		{"findings: a managed resource resolves its finding", testRecordResolves},
		{"findings: an ignore rule ignores a new finding", testRuleApplies},
		{"findings: an action checks the status that the caller read", testActChecks},
		{"findings: unseen findings resolve only in complete scopes", testResolveUnseen},
		{"findings: ignores end at their time", testExpireIgnores},
		{"findings: a scope with regions resolves its regions only", testResolveRegions},
		{"findings: a key with no account takes the scope of its sweep", testResolveNoAccount},
		{"findings: a person cancels a pending action", testCancelPending},
		{"findings: a pending action ends at its time", testExpirePending},
		{"findings: a stale fence refuses each write", testFindingsFenced},
	}
}

// matchingScan starts a sweep of kind k and moves it to ScanMatching. A cloud
// sweep needs a target, which it makes.
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
			OutOfScope:   0,
		},
	}

	if k == unmanaged.ScanCloud {
		target := targetOf(ns, "aws")
		err := h.Scans.CreateTarget(t.Context(), target, f, auditOf(ns, "namespace:manage-sweeps"))
		if err != nil && !errors.Is(err, unmanaged.ErrAlreadyExists) {
			t.Fatalf("create target: %v", err)
		}

		s.Target = target.Name
	}

	s, err := h.Scans.StartScan(t.Context(), s, f, auditOf(ns, "namespace:start-sweep"))
	must(t, "start sweep", err)

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
			auditOf(ns, "namespace:start-sweep"),
		)
		must(t, string(ev), err)
	}

	return s
}

func targetOf(ns, name string) unmanaged.Target {
	return unmanaged.Target{
		Namespace: namespace.Name(ns), Name: name, Pool: Pool,
		Scopes: []unmanaged.TargetScope{
			{Provider: "aws", Scope: "111111111111", Regions: nil},
			{Provider: "aws", Scope: "222222222222", Regions: nil},
			{Provider: "aws", Scope: "123456789012", Regions: nil},
		},
		Command: []string{"inventory-sweep"}, Every: 24 * time.Hour, Enabled: true,
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
		auditOf("acme", "namespace:start-sweep"),
	)
	must(t, "end first", err)

	if !ended.Status.Terminal() {
		t.Fatalf("sweep %s, want an end status", ended.Status)
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
		Scope:    "",
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
		auditOf("acme", "namespace:start-sweep"),
	)
	must(t, "end first", err)

	// The same key in the same sweep again changes nothing, so the second
	// observation comes from a second sweep.
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
	obs.Record = &unmanaged.Record{
		Provider: "aws",
		ID:       key.ID,
		Type:     "AWS::EC2::VPC",
		Tags:     nil,
		Region:   "",
		Name:     "",
		Scope:    "",
	}

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
			o.Record = &unmanaged.Record{
				Provider: k.Provider,
				ID:       k.ID,
				Type:     "t",
				Tags:     nil,
				Region:   "",
				Name:     "",
				Scope:    "",
			}
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
		auditOf("acme", "namespace:start-sweep"),
	)
	must(t, "end first", err)

	// The second sweep sees neither key. Only the account 111111111111 is
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
			OutOfScope:   0,
		},
	}
	second, err = h.Scans.StartScan(t.Context(), second, f, auditOf("acme", "namespace:start-sweep"))
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
			auditOf("acme", "namespace:start-sweep"),
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

// recordSeen records, in sweep s, the keys of records as seen by a cloud
// sweep that classifies each one as not in any unit.
func recordSeen(t *testing.T, h Harness, f namespace.Fence, s unmanaged.Scan, records ...unmanaged.Record) {
	t.Helper()

	obs := make([]unmanaged.Observation, 0, len(records))

	for i := range records {
		k := unmanaged.ResourceKey{Provider: records[i].Provider, ID: records[i].ID}
		o := observation(k, unmanaged.ClassNotInAnyUnit)
		o.Record = &records[i]
		obs = append(obs, o)
	}

	_, err := h.Findings.Record(t.Context(), s, obs, at(), f)
	must(t, "record", err)
}

// endScan moves the sweep s from matching to succeeded.
func endScan(t *testing.T, h Harness, f namespace.Fence, s unmanaged.Scan) {
	t.Helper()

	_, err := h.Scans.AdvanceScan(
		t.Context(),
		f.Namespace,
		s.ID,
		unmanaged.ScanEventMatched,
		unmanaged.ScanUpdate{
			Scopes: nil,
			Counts: nil,
			Reason: "",
		},
		f,
		auditOf(f.Namespace.String(), "namespace:start-sweep"),
	)
	must(t, "end sweep", err)
}

func rec(id, region, scope string) unmanaged.Record {
	return unmanaged.Record{Provider: "aws", ID: id, Type: "t", Tags: nil, Region: region, Name: "", Scope: scope}
}

func testResolveRegions(t *testing.T, h Harness) {
	t.Helper()

	f := h.Namespace(t, "acme")
	eu := rec("arn:aws:ec2:eu-west-1:123456789012:vpc/vpc-eu", "eu-west-1", "")
	us := rec("arn:aws:ec2:us-east-1:123456789012:vpc/vpc-us", "us-east-1", "")
	global := rec("arn:aws:iam::123456789012:role/ops", "", "")

	every := []unmanaged.ReportScope{{Provider: "aws", Scope: "123456789012", Complete: true, Error: "", Regions: nil}}
	first := matchingScan(t, h, f, unmanaged.ScanCloud, every)
	recordSeen(t, h, f, first, eu, us, global)
	endScan(t, h, f, first)

	// The second sweep reads eu-west-1 only and sees nothing. Only the
	// finding of eu-west-1 resolves: the sweep did not read us-east-1, and a
	// global resource resolves only by a sweep of every region.
	one := []unmanaged.ReportScope{{
		Provider: "aws", Scope: "123456789012", Complete: true, Error: "", Regions: []string{"eu-west-1"},
	}}
	second := matchingScan(t, h, f, unmanaged.ScanCloud, one)

	n, err := h.Findings.ResolveUnseen(t.Context(), second, at(), f)
	must(t, "resolve unseen", err)

	if n != 1 {
		t.Errorf("resolved %d, want 1", n)
	}

	for i, all := 0, listAll(t, h); i < len(all); i++ {
		fd := &all[i]
		want := unmanaged.StatusOpen
		if fd.Key.ID == eu.ID {
			want = unmanaged.StatusResolved
		}

		if fd.Status != want {
			t.Errorf("finding %s in %q is %s, want %s", fd.Key.ID, fd.Region, fd.Status, want)
		}
	}
}

func testResolveNoAccount(t *testing.T, h Harness) {
	t.Helper()

	f := h.Namespace(t, "acme")
	bucket := rec("arn:aws:s3:::logs", "eu-west-1", "111111111111")

	// A state check finds a bucket left behind. Its key names no account, so
	// the finding has no scope, and no sweep resolves it by not seeing it.
	check := matchingScan(t, h, f, unmanaged.ScanState, nil)
	_, err := h.Findings.Record(t.Context(), check, []unmanaged.Observation{
		observation(AWSKey("old"), unmanaged.ClassLeftBehind,
			unmanaged.Holder{Unit: Unit("acme", "gone"), Deleted: true, Resource: "", Type: ""}),
	}, at(), f)
	must(t, "record state", err)
	endScan(t, h, f, check)

	scopes := []unmanaged.ReportScope{{Provider: "aws", Scope: "111111111111", Complete: true, Error: "", Regions: nil}}
	first := matchingScan(t, h, f, unmanaged.ScanCloud, scopes)
	recordSeen(t, h, f, first, bucket)
	endScan(t, h, f, first)

	for i, all := 0, listAll(t, h); i < len(all); i++ {
		fd := &all[i]
		want := map[string]string{bucket.ID: "111111111111", AWSKey("old").ID: ""}[fd.Key.ID]
		if fd.Scope != want {
			t.Errorf("finding %s has scope %q, want %q", fd.Key.ID, fd.Scope, want)
		}
	}

	second := matchingScan(t, h, f, unmanaged.ScanCloud, scopes)

	n, err := h.Findings.ResolveUnseen(t.Context(), second, at(), f)
	must(t, "resolve unseen", err)

	for i, all := 0, listAll(t, h); i < len(all); i++ {
		fd := &all[i]
		want := unmanaged.StatusOpen
		if fd.Key.ID == bucket.ID {
			want = unmanaged.StatusResolved
		}

		if fd.Status != want {
			t.Errorf("finding %s is %s, want %s", fd.Key.ID, fd.Status, want)
		}
	}

	if n != 1 {
		t.Errorf("resolved %d, want 1", n)
	}
}

// pendingFinding makes one open finding in acme and starts an action on it
// at at().
func pendingFinding(t *testing.T, h Harness, f namespace.Fence) unmanaged.Finding {
	t.Helper()

	s := matchingScan(t, h, f, unmanaged.ScanState, nil)
	_, err := h.Findings.Record(t.Context(), s, []unmanaged.Observation{
		observation(AWSKey("x"), unmanaged.ClassLeftBehind,
			unmanaged.Holder{Unit: Unit("acme", "gone"), Deleted: true, Resource: "", Type: ""}),
	}, at(), f)
	must(t, "record", err)

	pending := &unmanaged.Pending{
		At: at(), Keep: nil, Target: nil, Action: unmanaged.ActionDelete, Job: "", By: "user:ada",
	}

	got, err := h.Findings.Act(t.Context(), "acme", firstFinding(t, h).ID, unmanaged.StatusOpen, unmanaged.EventAct,
		unmanaged.Change{Ignore: nil, Pending: pending}, f, auditOf("acme", "namespace:triage-unmanaged"))
	must(t, "act", err)

	if got.Status != unmanaged.StatusPending || got.Pending == nil {
		t.Fatalf("finding %+v, want pending", got)
	}

	return got
}

func testCancelPending(t *testing.T, h Harness) {
	t.Helper()

	f := h.Namespace(t, "acme")
	fd := pendingFinding(t, h, f)
	e := auditOf("acme", "namespace:triage-unmanaged")
	none := unmanaged.Change{Ignore: nil, Pending: nil}

	got, err := h.Findings.Act(t.Context(), "acme", fd.ID, unmanaged.StatusPending, unmanaged.EventCancel, none, f, e)
	must(t, "cancel", err)

	if got.Status != unmanaged.StatusOpen || got.Pending != nil {
		t.Errorf("finding %+v, want open with no pending action", got)
	}

	_, err = h.Findings.Act(t.Context(), "acme", fd.ID, unmanaged.StatusOpen, unmanaged.EventCancel, none, f, e)
	wantErr(t, "cancel an open finding", err, unmanaged.ErrTransition)
}

func testExpirePending(t *testing.T, h Harness) {
	t.Helper()

	f := h.Namespace(t, "acme")
	pendingFinding(t, h, f)

	n, err := h.Findings.ExpirePending(t.Context(), "acme", at().Add(unmanaged.MaxPending-time.Minute), f)
	must(t, "expire early", err)

	if n != 0 {
		t.Errorf("expired %d before the end, want 0", n)
	}

	n, err = h.Findings.ExpirePending(t.Context(), "acme", at().Add(unmanaged.MaxPending), f)
	must(t, "expire", err)

	if got := firstFinding(t, h); n != 1 || got.Status != unmanaged.StatusOpen || got.Pending != nil {
		t.Errorf("expired %d, finding %+v; want 1 and open", n, got)
	}

	_, err = h.Findings.ExpirePending(t.Context(), "acme", at(), stale(f))
	wantErr(t, "expire with a stale fence", err, namespace.ErrFenced)
}
