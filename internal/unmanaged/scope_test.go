package unmanaged_test

import (
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/unmanaged"
	"opendustpan.org/opendustpan/pkg/namespace"
)

func reportScope(scope string, regions ...string) unmanaged.ReportScope {
	return unmanaged.ReportScope{Provider: "aws", Scope: scope, Complete: true, Error: "", Regions: regions}
}

func failedScope(scope string) unmanaged.ReportScope {
	return unmanaged.ReportScope{Provider: "aws", Scope: scope, Complete: false, Error: "access denied", Regions: nil}
}

func TestCheckHeaderScopes(t *testing.T) {
	t.Parallel()

	target := []unmanaged.TargetScope{
		{Provider: "aws", Scope: "111111111111", Regions: nil},
		{Provider: "aws", Scope: "222222222222", Regions: []string{"eu-west-1", "eu-central-1"}},
	}

	cases := []struct {
		name   string
		header []unmanaged.ReportScope
		ok     bool
	}{
		{"the scopes of the target", []unmanaged.ReportScope{
			reportScope("111111111111"), reportScope("222222222222", "eu-west-1"),
		}, true},
		{"fewer scopes than the target", []unmanaged.ReportScope{reportScope("111111111111")}, true},
		{"one region of the target", []unmanaged.ReportScope{reportScope("222222222222", "eu-central-1")}, true},
		{"a scope of no target", []unmanaged.ReportScope{reportScope("333333333333")}, false},
		{"a region of no target", []unmanaged.ReportScope{reportScope("222222222222", "us-east-1")}, false},
		{"every region of a target with regions", []unmanaged.ReportScope{reportScope("222222222222")}, false},
		{"a scope twice", []unmanaged.ReportScope{failedScope("111111111111"), reportScope("111111111111")}, false},
		{"another provider", []unmanaged.ReportScope{{
			Provider: "gcp", Scope: "111111111111", Complete: true, Error: "", Regions: nil,
		}}, false},
	}

	for _, tc := range cases {
		err := unmanaged.CheckHeaderScopes(target, tc.header)
		if (err == nil) != tc.ok || (err != nil && !errors.Is(err, unmanaged.ErrInvalidReport)) {
			t.Errorf("%s: CheckHeaderScopes = %v, want ok %v", tc.name, err, tc.ok)
		}
	}
}

func TestRecordScope(t *testing.T) {
	t.Parallel()

	one := []unmanaged.ReportScope{reportScope("111111111111", "eu-west-1")}
	two := []unmanaged.ReportScope{reportScope("111111111111"), reportScope("222222222222")}

	cases := []struct {
		name   string
		header []unmanaged.ReportScope
		id     string
		region string
		scope  string
		want   string
		in     bool
	}{
		{"account of the key", two, "arn:aws:ec2:eu-west-1:222222222222:vpc/v", "eu-west-1", "", "222222222222", true},
		{"account of no scope", two, "arn:aws:ec2:eu-west-1:333333333333:vpc/v", "eu-west-1", "", "", false},
		{
			"a record that names another account", two, "arn:aws:ec2:eu-west-1:222222222222:vpc/v", "", "111111111111",
			"", false,
		},
		{"no account, one scope", one, "arn:aws:s3:::logs", "eu-west-1", "", "111111111111", true},
		{"no account, two scopes, named", two, "arn:aws:s3:::logs", "", "222222222222", "222222222222", true},
		{"no account, two scopes, not named", two, "arn:aws:s3:::logs", "", "", "", false},
		{"no account, a scope of no header", two, "arn:aws:s3:::logs", "", "333333333333", "", false},
		{
			"a region of the scope", one, "arn:aws:ec2:eu-west-1:111111111111:vpc/v", "eu-west-1", "", "111111111111",
			true,
		},
		{"a region outside the scope", one, "arn:aws:ec2:us-east-1:111111111111:vpc/v", "us-east-1", "", "", false},
		{"a global resource", one, "arn:aws:iam::111111111111:role/ops", "", "", "111111111111", true},
		{
			"the region of the key, none in the record", one, "arn:aws:ec2:eu-west-1:111111111111:vpc/v", "", "",
			"111111111111", true,
		},
		{
			"the region of the key outside the scope", one, "arn:aws:ec2:us-east-1:111111111111:vpc/v", "", "",
			"", false,
		},
		{
			"a record that names another region than its key", one, "arn:aws:ec2:us-east-1:111111111111:vpc/v",
			"eu-west-1", "", "", false,
		},
	}

	for _, tc := range cases {
		r := unmanaged.Record{
			Provider: "aws", ID: tc.id, Type: "t", Tags: nil, Region: tc.region, Name: "", Scope: tc.scope,
		}

		k, err := r.Key()
		if err != nil {
			t.Fatalf("%s: key: %v", tc.name, err)
		}

		got, in := unmanaged.RecordScope(tc.header, r, k)
		if in != tc.in || got.Scope != tc.want {
			t.Errorf("%s: RecordScope = %q, %v; want %q, %v", tc.name, got.Scope, in, tc.want, tc.in)
		}
	}
}

func TestCovers(t *testing.T) {
	t.Parallel()

	finding := func(scope, region string) unmanaged.Finding {
		return unmanaged.Finding{
			FirstSeen: time.Time{}, LastSeen: time.Time{}, Ignore: nil, Pending: nil, Tags: nil,
			Key: unmanaged.ResourceKey{Provider: "aws", ID: "arn:aws:s3:::logs"}, ID: "", Namespace: "",
			Kind: unmanaged.KindNotInAnyUnit, Status: unmanaged.StatusOpen, Evidence: unmanaged.EvidenceScan,
			Type: "", Scope: scope, Region: region, Name: "", Scan: "", Units: nil, Elsewhere: false,
		}
	}

	cases := []struct {
		name    string
		scope   unmanaged.ReportScope
		finding unmanaged.Finding
		want    bool
	}{
		{"every region", reportScope("1"), finding("1", "eu-west-1"), true},
		{"a region of the scope", reportScope("1", "eu-west-1"), finding("1", "eu-west-1"), true},
		{"a region outside the scope", reportScope("1", "eu-west-1"), finding("1", "us-east-1"), false},
		{"a global resource, every region", reportScope("1"), finding("1", ""), true},
		{"a global resource, some regions", reportScope("1", "eu-west-1"), finding("1", ""), false},
		{"another account", reportScope("2"), finding("1", "eu-west-1"), false},
		{"a scope that is not complete", failedScope("1"), finding("1", "eu-west-1"), false},
		{"a finding with no scope", reportScope("1"), finding("", "eu-west-1"), false},
	}

	for _, tc := range cases {
		if got := unmanaged.Covers(tc.scope, tc.finding); got != tc.want {
			t.Errorf("%s: Covers = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestStateCheckOffset(t *testing.T) {
	t.Parallel()

	starts := map[time.Duration]bool{}

	for i := range 200 {
		ns := namespace.Name("team-" + string(rune('a'+i%26)) + string(rune('a'+i/26)))
		off := unmanaged.StateCheckOffset(ns)

		if off < 0 || off >= 24*time.Hour || off%unmanaged.ScheduleTick != 0 {
			t.Fatalf("offset of %s = %v, want a tick of the day", ns, off)
		}

		if off != unmanaged.StateCheckOffset(ns) {
			t.Fatalf("offset of %s changes", ns)
		}

		starts[off] = true
	}

	// 200 namespaces over 96 ticks of the day: a spread that puts them all
	// on a few ticks is a broken hash.
	if len(starts) < 50 {
		t.Errorf("200 namespaces start on %d ticks, want 50 or more", len(starts))
	}
}
