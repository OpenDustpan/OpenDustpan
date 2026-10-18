package unmanaged_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/graph"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/unmanaged"
)

const bucketARN = "arn:aws:s3:::logs"

func TestNormalizeKey(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		in     unmanaged.ResourceKey
		wantID string
		scope  string
		ok     bool
	}{
		{
			"AWS ARN keeps its case",
			unmanaged.ResourceKey{Provider: "aws", ID: "arn:aws:ec2:eu-west-1:123456789012:vpc/vpc-0ABC"},
			"arn:aws:ec2:eu-west-1:123456789012:vpc/vpc-0ABC", "123456789012", true,
		},
		{"AWS ARN with no account", unmanaged.ResourceKey{Provider: "aws", ID: bucketARN}, bucketARN, "", true},
		{"AWS ID that is not an ARN", unmanaged.ResourceKey{Provider: "aws", ID: "vpc-0abc"}, "", "", false},
		{
			"Azure ID in lowercase with no trailing slash",
			unmanaged.ResourceKey{
				Provider: "azure",
				ID:       "/subscriptions/AAA/resourceGroups/RG/providers/Microsoft.Storage/storageAccounts/Logs/",
			},
			"/subscriptions/aaa/resourcegroups/rg/providers/microsoft.storage/storageaccounts/logs", "aaa", true,
		},
		{"Azure ID of another form", unmanaged.ResourceKey{Provider: "azure", ID: "rg/logs"}, "", "", false},
		{
			"Google Cloud full resource name",
			unmanaged.ResourceKey{
				Provider: "gcp",
				ID:       "//compute.googleapis.com/projects/p1/zones/z/instances/vm",
			},
			"//compute.googleapis.com/projects/p1/zones/z/instances/vm", "p1", true,
		},
		{
			"Google Cloud self link",
			unmanaged.ResourceKey{Provider: "gcp", ID: "https://www.googleapis.com/x"},
			"",
			"",
			false,
		},
		{
			"Kubernetes object",
			unmanaged.ResourceKey{Provider: "kubernetes", ID: "prod/apps/Deployment/web/api"},
			"prod/apps/Deployment/web/api", "prod", true,
		},
		{
			"Kubernetes object with no name",
			unmanaged.ResourceKey{Provider: "kubernetes", ID: "prod/apps/Deployment/web/"},
			"",
			"",
			false,
		},
		{
			"other provider is opaque",
			unmanaged.ResourceKey{Provider: "cloudflare", ID: "zone/abc"},
			"zone/abc",
			"",
			true,
		},
		{"provider in uppercase", unmanaged.ResourceKey{Provider: "AWS", ID: bucketARN}, "", "", false},
		{"ID with a space", unmanaged.ResourceKey{Provider: "other", ID: "a b"}, "", "", false},
		{"empty ID", unmanaged.ResourceKey{Provider: "other", ID: ""}, "", "", false},
		{
			"long ID",
			unmanaged.ResourceKey{Provider: "other", ID: strings.Repeat("a", unmanaged.MaxKeyIDBytes+1)},
			"",
			"",
			false,
		},
	}

	for _, tc := range cases {
		got, err := unmanaged.NormalizeKey(tc.in)
		if (err == nil) != tc.ok {
			t.Errorf("%s: error %v, want ok %v", tc.name, err, tc.ok)

			continue
		}

		if err != nil {
			if !errors.Is(err, unmanaged.ErrInvalidKey) {
				t.Errorf("%s: error %v, want ErrInvalidKey", tc.name, err)
			}

			continue
		}

		if got.ID != tc.wantID || unmanaged.Scope(got) != tc.scope {
			t.Errorf("%s: %q scope %q, want %q scope %q", tc.name, got.ID, unmanaged.Scope(got), tc.wantID, tc.scope)
		}
	}
}

func TestParseHeader(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		line string
		ok   bool
	}{
		{
			"valid",
			`{"format":"opendustpan-sweep-report","version":1,"scopes":[{"provider":"aws","scope":"123","regions":["eu-west-1"],"complete":true}]}`,
			true,
		},
		{
			"incomplete scope with its error",
			`{"format":"opendustpan-sweep-report","version":1,"scopes":[{"provider":"aws","scope":"123","complete":false,"error":"access denied in us-east-1"}]}`,
			true,
		},
		{
			"complete scope with an error",
			`{"format":"opendustpan-sweep-report","version":1,"scopes":[{"provider":"aws","scope":"123","complete":true,"error":"x"}]}`,
			false,
		},
		{
			"other format",
			`{"format":"csv","version":1,"scopes":[{"provider":"aws","scope":"1","complete":true}]}`,
			false,
		},
		{
			"newer version",
			`{"format":"opendustpan-sweep-report","version":2,"scopes":[{"provider":"aws","scope":"1","complete":true}]}`,
			false,
		},
		{"no scope", `{"format":"opendustpan-sweep-report","version":1,"scopes":[]}`, false},
		{
			"a scope twice",
			`{"format":"opendustpan-sweep-report","version":1,"scopes":[{"provider":"aws","scope":"1","complete":false,"error":"x"},{"provider":"aws","scope":"1","complete":true}]}`,
			false,
		},
		{
			"one scope name of two providers",
			`{"format":"opendustpan-sweep-report","version":1,"scopes":[{"provider":"aws","scope":"1","complete":true},{"provider":"gcp","scope":"1","complete":true}]}`,
			true,
		},
		{
			"unknown field",
			`{"format":"opendustpan-sweep-report","version":1,"scopes":[{"provider":"aws","scope":"1","complete":true}],"x":1}`,
			false,
		},
	}

	for _, tc := range cases {
		_, err := unmanaged.ParseHeader([]byte(tc.line))
		if (err == nil) != tc.ok || (err != nil && !errors.Is(err, unmanaged.ErrInvalidReport)) {
			t.Errorf("%s: error %v, want ok %v", tc.name, err, tc.ok)
		}
	}
}

func TestParseRecord(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		line  string
		keyID string
		ok    bool
	}{
		{
			"AWS bucket",
			`{"provider":"aws","id":"arn:aws:s3:::logs","type":"AWS::S3::Bucket","region":"eu-west-1","tags":{"team":"web"}}`,
			bucketARN,
			true,
		},
		{
			"Azure ID in normal form",
			`{"provider":"azure","id":"/subscriptions/A/resourceGroups/B","type":"Microsoft.Resources/resourceGroups"}`,
			"/subscriptions/a/resourcegroups/b",
			true,
		},
		{"no type", `{"provider":"aws","id":"arn:aws:s3:::logs"}`, "", false},
		{"invalid key", `{"provider":"aws","id":"logs","type":"AWS::S3::Bucket"}`, "", false},
		{"name with a line feed", `{"provider":"aws","id":"arn:aws:s3:::logs","type":"t","name":"a\nb"}`, "", false},
		{"unknown field", `{"provider":"aws","id":"arn:aws:s3:::logs","type":"t","arn":"x"}`, "", false},
		{"empty tag key", `{"provider":"aws","id":"arn:aws:s3:::logs","type":"t","tags":{"":"x"}}`, "", false},
	}

	for _, tc := range cases {
		rec, key, err := unmanaged.ParseRecord([]byte(tc.line))
		if (err == nil) != tc.ok {
			t.Errorf("%s: error %v, want ok %v", tc.name, err, tc.ok)

			continue
		}

		if err == nil && (key.ID != tc.keyID || rec.ID != tc.keyID) {
			t.Errorf("%s: key %q record %q, want %q", tc.name, key.ID, rec.ID, tc.keyID)
		}
	}
}

func TestRegion(t *testing.T) {
	t.Parallel()

	cases := []struct {
		key  unmanaged.ResourceKey
		want string
	}{
		{unmanaged.ResourceKey{Provider: "aws", ID: "arn:aws:ec2:eu-west-1:111111111111:vpc/v"}, "eu-west-1"},
		{unmanaged.ResourceKey{Provider: "aws", ID: "arn:aws:iam::111111111111:role/ops"}, ""},
		{unmanaged.ResourceKey{Provider: "aws", ID: bucketARN}, ""},
		{unmanaged.ResourceKey{
			Provider: "gcp", ID: "//compute.googleapis.com/projects/p/regions/europe-west1/subnetworks/s",
		}, "europe-west1"},
		{unmanaged.ResourceKey{
			Provider: "gcp", ID: "//compute.googleapis.com/projects/p/zones/europe-west1-b/instances/i",
		}, ""},
		{unmanaged.ResourceKey{Provider: "azure", ID: "/subscriptions/a/resourcegroups/b"}, ""},
	}

	for _, tc := range cases {
		if got := unmanaged.Region(tc.key); got != tc.want {
			t.Errorf("Region(%s) = %q, want %q", tc.key, got, tc.want)
		}
	}

	// A record with no region takes the region of its key.
	rec, _, err := unmanaged.ParseRecord(
		[]byte(`{"provider":"aws","id":"arn:aws:ec2:eu-west-1:111111111111:vpc/v","type":"AWS::EC2::VPC"}`),
	)
	if err != nil || rec.Region != "eu-west-1" {
		t.Errorf("record %+v, %v; want the region eu-west-1 of its key", rec, err)
	}
}

func holder(ns, id string, deleted bool) unmanaged.Holder {
	return unmanaged.Holder{
		Unit:     graph.Unit{Namespace: namespace.Name(ns), Tool: "opentofu", ID: id},
		Resource: "aws_s3_bucket.logs", Type: "aws_s3_bucket", Deleted: deleted,
	}
}

func TestClassify(t *testing.T) {
	t.Parallel()

	a, b := holder("acme", "a", false), holder("acme", "b", false)
	aRes2 := a
	aRes2.Resource = "aws_s3_bucket.copy"
	gone := holder("acme", "old", true)
	other := holder("other", "x", false)
	otherGone := holder("other", "y", true)

	linked := []namespace.Name{"other"}
	stranger := holder("stranger", "z", false)
	strangerGone := holder("stranger", "w", true)

	cases := []struct {
		name     string
		holders  []unmanaged.Holder
		linked   []namespace.Name
		want     unmanaged.Class
		observed bool
	}{
		{"one live unit", []unmanaged.Holder{a}, linked, unmanaged.ClassManaged, true},
		{"two live units", []unmanaged.Holder{a, b}, linked, unmanaged.ClassManagedTwice, true},
		{"two live units, no sweep", []unmanaged.Holder{a, b}, linked, unmanaged.ClassManagedTwice, false},
		{"one unit with two resources", []unmanaged.Holder{a, aRes2}, linked, unmanaged.ClassManaged, true},
		{
			"a live unit here and one in a linked namespace",
			[]unmanaged.Holder{a, other},
			linked,
			unmanaged.ClassManagedTwice,
			true,
		},
		{"a live unit and a deleted unit", []unmanaged.Holder{a, gone}, linked, unmanaged.ClassManaged, true},
		{"only in a linked namespace", []unmanaged.Holder{other}, linked, unmanaged.ClassElsewhere, true},
		{"only a deleted unit", []unmanaged.Holder{gone}, linked, unmanaged.ClassLeftBehind, false},
		{
			"a deleted unit here and a live unit in a linked namespace",
			[]unmanaged.Holder{gone, other},
			linked,
			unmanaged.ClassElsewhere,
			true,
		},
		{
			"only a deleted unit in a linked namespace",
			[]unmanaged.Holder{otherGone},
			linked,
			unmanaged.ClassElsewhere,
			true,
		},
		{"no holder, seen by a sweep", nil, linked, unmanaged.ClassNotInAnyUnit, true},
		{"no holder, no sweep", nil, linked, unmanaged.ClassNone, false},
		// A namespace that is not linked never changes the class.
		{
			"a live unit here and one in a namespace that is not linked",
			[]unmanaged.Holder{a, stranger},
			linked,
			unmanaged.ClassManaged,
			true,
		},
		{
			"only in a namespace that is not linked",
			[]unmanaged.Holder{stranger},
			linked,
			unmanaged.ClassNotInAnyUnit,
			true,
		},
		{
			"a deleted unit here and a live unit in a namespace that is not linked",
			[]unmanaged.Holder{gone, stranger},
			linked,
			unmanaged.ClassLeftBehind,
			true,
		},
		{
			"only a deleted unit in a namespace that is not linked",
			[]unmanaged.Holder{strangerGone},
			linked,
			unmanaged.ClassNone,
			false,
		},
		{"no link at all", []unmanaged.Holder{a, other}, nil, unmanaged.ClassManaged, true},
	}

	for _, tc := range cases {
		if got := unmanaged.Classify("acme", tc.linked, tc.observed, tc.holders); got != tc.want {
			t.Errorf("%s: Classify = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestClassFinding(t *testing.T) {
	t.Parallel()

	for _, c := range []unmanaged.Class{unmanaged.ClassManaged, unmanaged.ClassElsewhere, unmanaged.ClassNone} {
		if _, ok := c.Finding(); ok {
			t.Errorf("class %q makes a finding", c)
		}
	}

	if k, ok := unmanaged.ClassLeftBehind.Finding(); !ok || k != unmanaged.KindLeftBehind {
		t.Errorf("left behind makes %q, %v", k, ok)
	}
}

func TestKeysOf(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   unmanaged.StateResource
		want []string
	}{
		{
			"aws from arn",
			unmanaged.StateResource{
				Provider:   "aws",
				ID:         "logs",
				Attributes: map[string]string{"arn": bucketARN},
				Resource:   "",
				Type:       "",
				DataSource: false,
			},
			[]string{bucketARN},
		},
		{
			"aws with no arn",
			unmanaged.StateResource{
				Provider:   "aws",
				ID:         "logs",
				Attributes: nil,
				Resource:   "",
				Type:       "",
				DataSource: false,
			},
			nil,
		},
		{
			"azure from the ID",
			unmanaged.StateResource{
				Provider:   "azure",
				ID:         "/subscriptions/S/resourceGroups/RG",
				Attributes: nil,
				Resource:   "",
				Type:       "",
				DataSource: false,
			},
			[]string{"/subscriptions/s/resourcegroups/rg"},
		},
		{"gcp with both names", unmanaged.StateResource{Provider: "gcp", Attributes: map[string]string{
			"full_resource_name": "//storage.googleapis.com/projects/_/buckets/b",
			"fullResourceName":   "//storage.googleapis.com/projects/_/buckets/b",
		}, Resource: "", Type: "", ID: "", DataSource: false}, []string{"//storage.googleapis.com/projects/_/buckets/b"}},
		{
			"data source",
			unmanaged.StateResource{
				Provider:   "aws",
				DataSource: true,
				Attributes: map[string]string{"arn": bucketARN},
				Resource:   "",
				Type:       "",
				ID:         "",
			},
			nil,
		},
		{
			"provider with no rule",
			unmanaged.StateResource{
				Provider:   "cloudflare",
				ID:         "abc",
				Attributes: nil,
				Resource:   "",
				Type:       "",
				DataSource: false,
			},
			nil,
		},
	}

	for _, tc := range cases {
		var got []string
		for _, k := range unmanaged.KeysOf(tc.in) {
			got = append(got, k.ID)
		}

		if !slices.Equal(got, tc.want) {
			t.Errorf("%s: KeysOf = %v, want %v", tc.name, got, tc.want)
		}
	}
}
