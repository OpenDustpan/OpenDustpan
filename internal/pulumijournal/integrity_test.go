package pulumijournal_test

import (
	"encoding/json/v2"
	"errors"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/pulumijournal"
)

// integrityOutputMetadata holds the deployment metadata of a replay output.
type integrityOutputMetadata struct {
	Metadata map[string]any `json:"metadata"`
}

// integrityOutput holds the deployment of a replay output.
type integrityOutput struct {
	Deployment integrityOutputMetadata `json:"deployment"`
}

// errPlainIntegrity is a non-integrity error for the type query test.
var errPlainIntegrity = errors.New("plain failure")

// integrityErrorOf decodes the integrity_error member of a replay output. It
// returns the member and whether the output holds one.
func integrityErrorOf(t *testing.T, got string) (map[string]any, bool) {
	t.Helper()
	var doc integrityOutput
	if err := json.Unmarshal([]byte(got), &doc); err != nil {
		t.Fatalf("decode output: %v", err)
	}
	if doc.Deployment.Metadata == nil {
		return nil, false
	}
	raw, ok := doc.Deployment.Metadata["integrity_error"]
	if !ok {
		return nil, false
	}
	member, ok := raw.(map[string]any)
	if !ok {
		t.Fatalf("integrity_error is %T, want an object", raw)
	}
	return member, true
}

// TestReplayPassWritesNoIntegrityError checks that a good update writes no
// integrity_error member.
func TestReplayPassWritesNoIntegrityError(t *testing.T) {
	t.Parallel()
	got, err := replay(baseJSON(resourceJSON("urn:a")), batch())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := integrityErrorOf(t, got); ok {
		t.Fatalf("good update wrote integrity_error:\n%s", got)
	}
}

// TestReplayFailureSetsIntegrityError checks that a resource whose parent is
// missing writes integrity_error with the schema version and the error text, and
// without a command or environment variables.
func TestReplayFailureSetsIntegrityError(t *testing.T) {
	t.Parallel()
	child := `{"urn":"urn:child","custom":true,"type":"t:mod:T","parent":"urn:missing"}`
	got, err := replay(baseJSON(child), batch())
	if err != nil {
		t.Fatal(err)
	}
	member, ok := integrityErrorOf(t, got)
	if !ok {
		t.Fatalf("bad update wrote no integrity_error:\n%s", got)
	}
	if member["version"] != "3" {
		t.Fatalf("integrity_error version is %v, want %q", member["version"], "3")
	}
	text, ok := member["error"].(string)
	if !ok || !strings.Contains(text, "missing parent") {
		t.Fatalf("integrity_error error is %v, want it to name the missing parent", member["error"])
	}
	for _, key := range []string{"command", "env_vars"} {
		if _, has := member[key]; has {
			t.Fatalf("integrity_error holds %q, want only the version and the error text", key)
		}
	}
	if len(member) != 2 {
		t.Fatalf("integrity_error holds %d members, want 2", len(member))
	}
}

// TestReplayGoodUpdateClearsStaleIntegrityError checks that a good update drops an
// integrity_error that the base still carries.
func TestReplayGoodUpdateClearsStaleIntegrityError(t *testing.T) {
	t.Parallel()
	base := `{"manifest":{"time":"2026-01-01T00:00:00Z","magic":"","version":""},` +
		`"metadata":{"integrity_error":{"version":"3","error":"old failure",` +
		`"command":"old command","env_vars":{"OLD":"1"}}},` +
		`"resources":[` + resourceJSON("urn:a") + `]}`
	got, err := replay(base, batch())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := integrityErrorOf(t, got); ok {
		t.Fatalf("good update kept the stale integrity_error:\n%s", got)
	}
	if strings.Contains(got, "old command") {
		t.Fatalf("good update kept the stale command:\n%s", got)
	}
}

// validIntegrityResource returns a resource that passes the integrity check.
func validIntegrityResource(urn string) pulumijournal.IntegrityResource {
	return pulumijournal.IntegrityResource{
		URN:                  urn,
		ResourceType:         "pkg:index:Type",
		Custom:               true,
		ID:                   "id-1",
		Provider:             "",
		Parent:               "",
		Dependencies:         nil,
		PropertyDependencies: nil,
		DeletedWith:          "",
		Delete:               false,
		PendingReplacement:   false,
	}
}

// integrityDeploymentOf returns a deployment with an empty manifest, which passes
// the magic check, holding the given resources and snippets.
func integrityDeploymentOf(
	resources []pulumijournal.IntegrityResource,
	snippets []pulumijournal.IntegritySnippet,
) *pulumijournal.IntegrityDeployment {
	return &pulumijournal.IntegrityDeployment{
		Manifest:  pulumijournal.IntegrityManifest{Magic: "", Version: ""},
		Resources: resources,
		Snippets:  snippets,
		Metadata:  nil,
	}
}

// checkIntegrityError runs the check and wants an error holding want that is an
// integrity error.
func checkIntegrityError(
	t *testing.T,
	deployment *pulumijournal.IntegrityDeployment,
	want string,
) {
	t.Helper()
	err := pulumijournal.VerifyIntegrity(deployment)
	if err == nil {
		t.Fatalf("check passed, want error holding %q", want)
	}
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("error is %q, want it to hold %q", err.Error(), want)
	}
	got, ok := pulumijournal.AsSnapshotIntegrityError(err)
	if !ok {
		t.Fatalf("error %v is not an integrity error", err)
	}
	if !strings.Contains(got.Error(), want) {
		t.Fatalf("integrity error is %q, want it to hold %q", got.Error(), want)
	}
}

// TestVerifyIntegrityPassesAGoodDeployment checks that a well formed deployment passes.
func TestVerifyIntegrityPassesAGoodDeployment(t *testing.T) {
	t.Parallel()
	provider := validIntegrityResource("urn:pulumi:dev::app::pulumi:providers:aws::default")
	provider.ResourceType = "pulumi:providers:aws"
	consumer := validIntegrityResource("urn:pulumi:dev::app::aws:s3/bucket:Bucket::b")
	consumer.Provider = "urn:pulumi:dev::app::pulumi:providers:aws::default::id-1"
	consumer.Parent = "urn:pulumi:dev::app::pulumi:pulumi:Stack::app"
	root := validIntegrityResource("urn:pulumi:dev::app::pulumi:pulumi:Stack::app")
	deployment := integrityDeploymentOf(
		[]pulumijournal.IntegrityResource{provider, root, consumer},
		[]pulumijournal.IntegritySnippet{{UUID: "e970a91d-4f4c-5793-8d27-dd27a0d96cf7"}},
	)
	if err := pulumijournal.VerifyIntegrity(deployment); err != nil {
		t.Fatalf("good deployment: %v", err)
	}
	if err := pulumijournal.VerifyIntegrity(nil); err != nil {
		t.Fatalf("missing deployment: %v", err)
	}
}

// TestVerifyIntegrityReportsEachFailure checks each error that the check reports.
func TestVerifyIntegrityReportsEachFailure(t *testing.T) {
	t.Parallel()
	providerURN := "urn:pulumi:dev::app::pulumi:providers:aws::default"
	providerRef := providerURN + "::id-1"
	tests := []struct {
		name      string
		resources []pulumijournal.IntegrityResource
		snippets  []pulumijournal.IntegritySnippet
		manifest  pulumijournal.IntegrityManifest
		want      string
	}{
		{
			name:      "magic mismatch",
			resources: []pulumijournal.IntegrityResource{validIntegrityResource("urn:a")},
			snippets:  nil,
			manifest:  pulumijournal.IntegrityManifest{Magic: "bad", Version: "test"},
			want:      "magic cookie mismatch",
		},
		{
			name: "missing urn",
			resources: []pulumijournal.IntegrityResource{
				func() pulumijournal.IntegrityResource {
					res := validIntegrityResource("")
					res.URN = ""
					return res
				}(),
			},
			snippets: nil,
			manifest: pulumijournal.IntegrityManifest{Magic: "", Version: ""},
			want:     "missing required 'urn' field",
		},
		{
			name: "missing type",
			resources: []pulumijournal.IntegrityResource{
				func() pulumijournal.IntegrityResource {
					res := validIntegrityResource("urn:a")
					res.ResourceType = ""
					return res
				}(),
			},
			snippets: nil,
			manifest: pulumijournal.IntegrityManifest{Magic: "", Version: ""},
			want:     "missing required 'type' field",
		},
		{
			name: "custom false with ID",
			resources: []pulumijournal.IntegrityResource{
				func() pulumijournal.IntegrityResource {
					res := validIntegrityResource("urn:a")
					res.Custom = false
					return res
				}(),
			},
			snippets: nil,
			manifest: pulumijournal.IntegrityManifest{Magic: "", Version: ""},
			want:     "custom false but non-empty ID",
		},
		{
			name: "provider not referenceable",
			resources: []pulumijournal.IntegrityResource{
				func() pulumijournal.IntegrityResource {
					res := validIntegrityResource("not-a-urn")
					res.ResourceType = "pulumi:providers:aws"
					return res
				}(),
			},
			snippets: nil,
			manifest: pulumijournal.IntegrityManifest{Magic: "", Version: ""},
			want:     "is not referenceable",
		},
		{
			name: "provider reference does not parse",
			resources: []pulumijournal.IntegrityResource{
				func() pulumijournal.IntegrityResource {
					res := validIntegrityResource("urn:a")
					res.Provider = "not-a-reference"
					return res
				}(),
			},
			snippets: nil,
			manifest: pulumijournal.IntegrityManifest{Magic: "", Version: ""},
			want:     "failed to parse provider reference",
		},
		{
			name: "unknown provider",
			resources: []pulumijournal.IntegrityResource{
				func() pulumijournal.IntegrityResource {
					res := validIntegrityResource("urn:a")
					res.Provider = providerRef
					return res
				}(),
			},
			snippets: nil,
			manifest: pulumijournal.IntegrityManifest{Magic: "", Version: ""},
			want:     "refers to unknown provider",
		},
		{
			name: "parent comes after",
			resources: []pulumijournal.IntegrityResource{
				func() pulumijournal.IntegrityResource {
					res := validIntegrityResource("urn:child")
					res.Parent = "urn:parent"
					return res
				}(),
				validIntegrityResource("urn:parent"),
			},
			snippets: nil,
			manifest: pulumijournal.IntegrityManifest{Magic: "", Version: ""},
			want:     "comes after it",
		},
		{
			name: "missing parent",
			resources: []pulumijournal.IntegrityResource{
				func() pulumijournal.IntegrityResource {
					res := validIntegrityResource("urn:child")
					res.Parent = "urn:gone"
					return res
				}(),
			},
			snippets: nil,
			manifest: pulumijournal.IntegrityManifest{Magic: "", Version: ""},
			want:     "missing parent",
		},
		{
			name: "dependency comes after",
			resources: []pulumijournal.IntegrityResource{
				func() pulumijournal.IntegrityResource {
					res := validIntegrityResource("urn:a")
					res.Dependencies = []string{"urn:b"}
					return res
				}(),
				validIntegrityResource("urn:b"),
			},
			snippets: nil,
			manifest: pulumijournal.IntegrityManifest{Magic: "", Version: ""},
			want:     "comes after it",
		},
		{
			name: "missing dependency",
			resources: []pulumijournal.IntegrityResource{
				func() pulumijournal.IntegrityResource {
					res := validIntegrityResource("urn:a")
					res.Dependencies = []string{"urn:gone"}
					return res
				}(),
			},
			snippets: nil,
			manifest: pulumijournal.IntegrityManifest{Magic: "", Version: ""},
			want:     "missing dependency",
		},
		{
			name: "property dependency comes after",
			resources: []pulumijournal.IntegrityResource{
				func() pulumijournal.IntegrityResource {
					res := validIntegrityResource("urn:a")
					res.PropertyDependencies = map[string][]string{"prop": {"urn:b"}}
					return res
				}(),
				validIntegrityResource("urn:b"),
			},
			snippets: nil,
			manifest: pulumijournal.IntegrityManifest{Magic: "", Version: ""},
			want:     "comes after it",
		},
		{
			name: "missing property dependency",
			resources: []pulumijournal.IntegrityResource{
				func() pulumijournal.IntegrityResource {
					res := validIntegrityResource("urn:a")
					res.PropertyDependencies = map[string][]string{"prop": {"urn:gone"}}
					return res
				}(),
			},
			snippets: nil,
			manifest: pulumijournal.IntegrityManifest{Magic: "", Version: ""},
			want:     "missing property dependency",
		},
		{
			name: "deleted with comes after",
			resources: []pulumijournal.IntegrityResource{
				func() pulumijournal.IntegrityResource {
					res := validIntegrityResource("urn:a")
					res.DeletedWith = "urn:b"
					return res
				}(),
				validIntegrityResource("urn:b"),
			},
			snippets: nil,
			manifest: pulumijournal.IntegrityManifest{Magic: "", Version: ""},
			want:     "comes after it",
		},
		{
			name: "missing deleted with",
			resources: []pulumijournal.IntegrityResource{
				func() pulumijournal.IntegrityResource {
					res := validIntegrityResource("urn:a")
					res.DeletedWith = "urn:gone"
					return res
				}(),
			},
			snippets: nil,
			manifest: pulumijournal.IntegrityManifest{Magic: "", Version: ""},
			want:     "which is missing",
		},
		{
			name: "duplicate resource",
			resources: []pulumijournal.IntegrityResource{
				validIntegrityResource("urn:a"),
				validIntegrityResource("urn:a"),
			},
			snippets: nil,
			manifest: pulumijournal.IntegrityManifest{Magic: "", Version: ""},
			want:     "duplicate resource",
		},
		{
			// Ports the "missing" case of TestVerifyIntegrity_SnippetUUID in
			// pkg/resource/stack/snapshot/integrity_test.go.
			name:      "snippet without UUID",
			resources: []pulumijournal.IntegrityResource{validIntegrityResource("urn:a")},
			snippets:  []pulumijournal.IntegritySnippet{{UUID: ""}},
			manifest:  pulumijournal.IntegrityManifest{Magic: "", Version: ""},
			want:      "missing required 'uuid' field",
		},
		{
			// Ports the "duplicate" case of TestVerifyIntegrity_SnippetUUID in
			// pkg/resource/stack/snapshot/integrity_test.go.
			name:      "duplicate snippet UUID",
			resources: []pulumijournal.IntegrityResource{validIntegrityResource("urn:a")},
			snippets: []pulumijournal.IntegritySnippet{
				{UUID: "e970a91d-4f4c-5793-8d27-dd27a0d96cf7"},
				{UUID: "e970a91d-4f4c-5793-8d27-dd27a0d96cf7"},
			},
			manifest: pulumijournal.IntegrityManifest{Magic: "", Version: ""},
			want:     "duplicate snippet uuid",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			deployment := &pulumijournal.IntegrityDeployment{
				Manifest:  tt.manifest,
				Resources: tt.resources,
				Snippets:  tt.snippets,
				Metadata:  nil,
			}
			checkIntegrityError(t, deployment, tt.want)
		})
	}
}

// TestVerifyIntegrityAcceptsMarkedDuplicates checks that a repeated URN passes when
// all or all but one of its states are marked for deletion.
func TestVerifyIntegrityAcceptsMarkedDuplicates(t *testing.T) {
	t.Parallel()
	oneDeleted := []pulumijournal.IntegrityResource{
		validIntegrityResource("urn:a"),
		func() pulumijournal.IntegrityResource {
			res := validIntegrityResource("urn:a")
			res.Delete = true
			return res
		}(),
	}
	if err := pulumijournal.VerifyIntegrity(integrityDeploymentOf(oneDeleted, nil)); err != nil {
		t.Fatalf("one state deleted: %v", err)
	}
	bothDeleted := []pulumijournal.IntegrityResource{
		func() pulumijournal.IntegrityResource {
			res := validIntegrityResource("urn:a")
			res.Delete = true
			return res
		}(),
		func() pulumijournal.IntegrityResource {
			res := validIntegrityResource("urn:a")
			res.Delete = true
			return res
		}(),
	}
	if err := pulumijournal.VerifyIntegrity(integrityDeploymentOf(bothDeleted, nil)); err != nil {
		t.Fatalf("both states deleted: %v", err)
	}
}

// TestVerifyIntegrityAcceptsPendingProvider checks that a resource pending
// replacement may name a provider that the deployment does not hold.
func TestVerifyIntegrityAcceptsPendingProvider(t *testing.T) {
	t.Parallel()
	res := validIntegrityResource("urn:a")
	res.Provider = "urn:pulumi:dev::app::pulumi:providers:aws::default::id-9"
	res.PendingReplacement = true
	deployment := integrityDeploymentOf([]pulumijournal.IntegrityResource{res}, nil)
	if err := pulumijournal.VerifyIntegrity(deployment); err != nil {
		t.Fatalf("pending replacement with unknown provider: %v", err)
	}
}

// TestVerifyIntegritySnippetReferencesNeedNotExist ports the upstream snippet case
// in pkg/resource/stack/snapshot/integrity_test.go: a snippet may refer to a URN
// that the deployment does not hold.
func TestVerifyIntegritySnippetReferencesNeedNotExist(t *testing.T) {
	t.Parallel()
	deployment := integrityDeploymentOf(
		nil,
		[]pulumijournal.IntegritySnippet{{UUID: "e970a91d-4f4c-5793-8d27-dd27a0d96cf7"}},
	)
	if err := pulumijournal.VerifyIntegrity(deployment); err != nil {
		t.Fatalf("snippet with a missing reference: %v", err)
	}
}

// TestSnapshotIntegrityErrorHelpers checks the error helpers: formatting, the
// read-time copies and the type query.
func TestSnapshotIntegrityErrorHelpers(t *testing.T) {
	t.Parallel()
	err := pulumijournal.SnapshotIntegrityErrorf("bad resource %s", "urn:a")
	integrityErr, ok := pulumijournal.AsSnapshotIntegrityError(err)
	if !ok {
		t.Fatalf("formatted error is not an integrity error: %v", err)
	}
	if integrityErr.Op != pulumijournal.SnapshotIntegrityWrite {
		t.Fatalf("operation is %v, want the write value", integrityErr.Op)
	}
	if !strings.Contains(integrityErr.Error(), "bad resource urn:a") {
		t.Fatalf("error is %q, want it to hold the message", integrityErr.Error())
	}
	metadata := &pulumijournal.IntegrityErrorMetadata{Version: "3", Error: "bad resource urn:a"}
	deployment := &pulumijournal.IntegrityDeployment{
		Manifest:  pulumijournal.IntegrityManifest{Magic: "", Version: ""},
		Resources: nil,
		Snippets:  nil,
		Metadata:  metadata,
	}
	read := integrityErr.ForRead(deployment)
	if read.Op != pulumijournal.SnapshotIntegrityRead {
		t.Fatalf("read operation is %v, want the read value", read.Op)
	}
	if read.Metadata != metadata {
		t.Fatalf("read metadata is %+v, want %+v", read.Metadata, metadata)
	}
	fixed := integrityErr.ForReadWithMetadata(metadata)
	if fixed.Op != pulumijournal.SnapshotIntegrityRead || fixed.Metadata != metadata {
		t.Fatalf("fixed read is %+v, want the read value with the metadata", fixed)
	}
	if got, ok := pulumijournal.AsSnapshotIntegrityError(errPlainIntegrity); ok {
		t.Fatalf("plain error %v answers as an integrity error", got)
	}
}
