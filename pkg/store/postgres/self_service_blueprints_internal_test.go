package postgres

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/selfservice"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/internal/selfservicestore"
)

func selfServiceTestBlueprint(name string) selfservice.Blueprint {
	module := selfservice.Module{
		Source: "registry.example.com/platform/postgres/aws", Version: "2.4.1",
		Connection: "", Repository: "", Directory: "", Revision: "",
	}

	return selfservice.Blueprint{
		Program: selfservice.Program{Module: &module, Component: nil},
		Platform: selfservice.Platform{
			Values: map[string]json.RawMessage{"backup_retention_days": json.RawMessage(`14`)}, Profile: "", Pool: "",
		},
		Delivery: selfservice.Delivery{
			Git: nil, Default: selfservice.HomeOpenDustpan, Homes: []selfservice.HomeKind{selfservice.HomeOpenDustpan},
		},
		Name: name, Title: "PostgreSQL", Description: "", Tool: "opentofu", ExprVersion: "v1.17.8",
		UnitName: "'databases/' + request.name", SourcePrincipal: "",
		Inputs: []selfservice.Input{{
			Choice: nil, Name: "size", Title: "", Description: "", Type: selfservice.TypeString, Maps: "instance_size",
			Default: json.RawMessage(`"small"`), Validate: nil, Required: false,
		}},
		Policy: nil, Quotas: nil, SchemaVersion: 1,
	}
}

func selfServiceTestEntry() audit.Entry {
	return audit.Entry{
		At: time.Now().UTC(), Namespace: "acme", Subject: "alice", IDPSubject: "", TokenID: "",
		Action: string(selfservice.ActionBlueprintPublish), Object: "acme/postgres", Version: "", ValuePath: "",
		ValueCount: nil, Changes: nil, SourceIP: "", UserAgent: "", Channel: audit.ChannelAPI,
		Kind: audit.KindChange, Outcome: audit.OutcomeSuccess, RequestID: "", ObjectID: "", PrevHash: "",
		Hash: "", Sequence: 0, FenceVersion: 0, Reason: "", PrincipalKind: "", JobID: "",
	}
}

// seedSelfServiceBlueprint makes the namespace acme with the blueprint postgres:
// one draft at version 1 and one published version.
func seedSelfServiceBlueprint(t *testing.T, s *Store) {
	t.Helper()

	fence := ensureInternalNamespace(t, s, "acme")
	store := s.SelfServiceBlueprints()
	ref := selfservice.BlueprintRef{Namespace: "acme", Name: "postgres"}
	b := selfServiceTestBlueprint("postgres")
	e := []audit.Entry{selfServiceTestEntry()}

	if _, err := store.PutDraft(t.Context(), b, 0, fence, e); err != nil {
		t.Fatalf("put draft: %v", err)
	}

	p := selfservice.Publication{At: time.Now().UTC(), Ref: ref, By: "platform", Blueprint: b, DraftVersion: 1}
	if _, err := store.Publish(t.Context(), p, fence, e); err != nil {
		t.Fatalf("publish: %v", err)
	}
}

func ensureInternalNamespace(t *testing.T, s *Store, name namespace.Name) namespace.Fence {
	t.Helper()

	ns := namespace.Namespace{
		ID:                  namespace.NewID(),
		CreatedAt:           time.UnixMilli(1_700_000_000_000).UTC(),
		Name:                name,
		PrimaryInstallation: "eu-west",
		FailoverVersion:     1,
		Overrides:           namespace.Overrides{Forced: false, Unlocked: false},
	}
	if err := s.CreateNamespace(t.Context(), ns); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	return ns.Fence()
}

// TestSelfServiceBlueprintReadRefusesHostileRows proves that a row which another
// version or a restore wrote, and which fails the rules of a blueprint, is
// never served, and that the restore check refuses the same rows.
func TestSelfServiceBlueprintReadRefusesHostileRows(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		update string
		draft  bool
	}{
		{
			"a draft with an unknown field",
			`UPDATE self_service_blueprints SET draft_json = '{"name":"postgres","x":1}'`,
			true,
		},
		{"a draft that is not JSON", `UPDATE self_service_blueprints SET draft_json = 'not json'`, true},
		{
			"a draft of another blueprint",
			`UPDATE self_service_blueprints SET draft_json = replace(draft_json, '"name":"postgres"', '"name":"redis"')`,
			true,
		},
		{"a version that is not JSON", `UPDATE self_service_blueprint_versions SET document_json = '{'`, false},
		{
			"a version with an unknown field",
			`UPDATE self_service_blueprint_versions SET document_json = '{"name":"postgres","x":1}'`,
			false,
		},
		{
			"a version with a title that is empty",
			`UPDATE self_service_blueprint_versions SET document_json = replace(document_json, '"title":"PostgreSQL"', '"title":""')`,
			false,
		},
		{
			"a version of another blueprint",
			`UPDATE self_service_blueprint_versions SET document_json = replace(document_json, '"name":"postgres"', '"name":"redis"')`,
			false,
		},
		{"a version number above the limit", `UPDATE self_service_blueprint_versions SET number = 1001`, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := openForSavedViews(t)
			seedSelfServiceBlueprint(t, s)

			err := checkStagedSelfServiceBlueprints(t.Context(), s.pool, currentSchema(t, s))
			if err != nil {
				t.Fatalf("check of good rows = %v, want nil", err)
			}

			if _, err := s.pool.Exec(t.Context(), tc.update); err != nil {
				t.Fatalf("change the row: %v", err)
			}

			switch {
			case tc.draft:
				assertSelfServiceDraftRefused(t, s.SelfServiceBlueprints())
			case strings.Contains(tc.update, "number = 1001"):
				assertSelfServiceVersionRefused(t, s.SelfServiceBlueprints(), 1001)
			default:
				assertSelfServiceVersionRefused(t, s.SelfServiceBlueprints(), 1)
			}

			staging := currentSchema(t, s)

			if err := checkStagedSelfServiceBlueprints(t.Context(), s.pool, staging); !errors.Is(
				err,
				backup.ErrCorrupt,
			) {
				t.Fatalf("check = %v, want backup.ErrCorrupt", err)
			}
		})
	}
}

// TestCheckStagedSelfServiceBlueprintsRefusesAMissingNamespace proves that a
// blueprint and a version whose namespace the backup does not hold are corrupt.
// An inner join would skip such a row, and the restore would bring it in.
func TestCheckStagedSelfServiceBlueprintsRefusesAMissingNamespace(t *testing.T) {
	t.Parallel()

	for _, table := range []string{"self_service_blueprints", "self_service_blueprint_versions"} {
		t.Run(table, func(t *testing.T) {
			t.Parallel()

			s := openForSavedViews(t)
			seedSelfServiceBlueprint(t, s)

			// The foreign key refuses this change, so the test drops the key in
			// its own schema first.
			for _, stmt := range []string{
				`ALTER TABLE self_service_blueprint_versions DROP CONSTRAINT self_service_blueprint_versions_namespace_id_name_fkey`,
				`ALTER TABLE ` + table + ` DROP CONSTRAINT IF EXISTS ` + table + `_namespace_id_fkey`,
				`UPDATE ` + table + ` SET namespace_id = 'ns-gone'`,
			} {
				if _, execErr := s.pool.Exec(t.Context(), stmt); execErr != nil {
					t.Fatalf("%s: %v", stmt, execErr)
				}
			}

			err := checkStagedSelfServiceBlueprints(t.Context(), s.pool, currentSchema(t, s))
			if !errors.Is(err, backup.ErrCorrupt) || !strings.Contains(err.Error(), "has no namespace") {
				t.Fatalf("check = %v, want ErrCorrupt for a missing namespace", err)
			}
		})
	}
}

// assertSelfServiceDraftRefused proves that the store serves no draft of ref.
func assertSelfServiceDraftRefused(t *testing.T, store SelfServiceBlueprintStore) {
	t.Helper()

	ref := selfservice.BlueprintRef{Namespace: "acme", Name: "postgres"}
	if _, _, err := store.Draft(t.Context(), ref); !errors.Is(err, selfservicestore.ErrCorrupt) {
		t.Fatalf("Draft() = %v, want ErrCorrupt", err)
	}
}

// assertSelfServiceVersionRefused proves that the store serves neither version n
// nor the list of versions.
func assertSelfServiceVersionRefused(t *testing.T, store SelfServiceBlueprintStore, n int) {
	t.Helper()

	ref := selfservice.BlueprintRef{Namespace: "acme", Name: "postgres"}
	if _, err := store.Version(t.Context(), ref, n); !errors.Is(err, selfservicestore.ErrCorrupt) {
		t.Fatalf("Version(%d) = %v, want ErrCorrupt", n, err)
	}

	if _, err := store.ListBlueprints(t.Context(), "acme"); !errors.Is(err, selfservicestore.ErrCorrupt) {
		t.Fatalf("ListBlueprints() = %v, want ErrCorrupt", err)
	}
}
