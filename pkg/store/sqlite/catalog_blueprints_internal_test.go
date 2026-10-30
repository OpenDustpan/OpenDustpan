package sqlite

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/catalog"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/internal/catalogstore"
)

func catalogTestBlueprint(name string) catalog.Blueprint {
	module := catalog.Module{
		Source: "registry.example.com/platform/postgres/aws", Version: "2.4.1",
		Connection: "", Repository: "", Directory: "", Revision: "",
	}

	return catalog.Blueprint{
		Program: catalog.Program{Module: &module, Component: nil},
		Platform: catalog.Platform{
			Values: map[string]json.RawMessage{"backup_retention_days": json.RawMessage(`14`)}, Profile: "", Pool: "",
		},
		Delivery: catalog.Delivery{
			Git: nil, Default: catalog.HomeOpenDustpan, Homes: []catalog.HomeKind{catalog.HomeOpenDustpan},
		},
		Name: name, Title: "PostgreSQL", Description: "", Tool: "opentofu", ExprVersion: "v1.17.8",
		UnitName: "'databases/' + request.name", SourcePrincipal: "",
		Inputs: []catalog.Input{{
			Choice: nil, Name: "size", Title: "", Description: "", Type: catalog.TypeString, Maps: "instance_size",
			Default: json.RawMessage(`"small"`), Validate: nil, Required: false,
		}},
		Policy: nil, Quotas: nil, SchemaVersion: 1,
	}
}

func catalogTestEntry() audit.Entry {
	return audit.Entry{
		At: time.Now().UTC(), Namespace: "acme", Subject: "alice", IDPSubject: "", TokenID: "",
		Action: string(catalog.ActionBlueprintPublish), Object: "acme/postgres", Version: "", ValuePath: "",
		ValueCount: nil, Changes: nil, SourceIP: "", UserAgent: "", Channel: audit.ChannelAPI,
		Kind: audit.KindChange, Outcome: audit.OutcomeSuccess, RequestID: "", ObjectID: "", PrevHash: "",
		Hash: "", Sequence: 0, FenceVersion: 0, Reason: "", PrincipalKind: "", JobID: "",
	}
}

// seedCatalogBlueprint makes the namespace acme with the blueprint postgres:
// one draft at version 1 and one published version.
func seedCatalogBlueprint(t *testing.T, s *Store) {
	t.Helper()

	fence := ensureInternalNamespace(t, s, "acme")
	store := s.CatalogBlueprints()
	ref := catalog.BlueprintRef{Namespace: "acme", Name: "postgres"}
	b := catalogTestBlueprint("postgres")
	e := []audit.Entry{catalogTestEntry()}

	if _, err := store.PutDraft(t.Context(), b, 0, fence, e); err != nil {
		t.Fatalf("put draft: %v", err)
	}

	p := catalog.Publication{At: time.Now().UTC(), Ref: ref, By: "platform", Blueprint: b, DraftVersion: 1}
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

// TestCatalogBlueprintReadRefusesHostileRows proves that a row which another
// version or a restore wrote, and which fails the rules of a blueprint, is
// never served, and that the restore check refuses the same rows.
func TestCatalogBlueprintReadRefusesHostileRows(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		update string
		draft  bool
	}{
		{
			"a draft with an unknown field",
			`UPDATE catalog_blueprints SET draft_json = '{"name":"postgres","x":1}'`,
			true,
		},
		{"a draft that is not JSON", `UPDATE catalog_blueprints SET draft_json = 'not json'`, true},
		{
			"a draft of another blueprint",
			`UPDATE catalog_blueprints SET draft_json = replace(draft_json, '"name":"postgres"', '"name":"redis"')`,
			true,
		},
		{"a version that is not JSON", `UPDATE catalog_blueprint_versions SET document_json = '{'`, false},
		{
			"a version with an unknown field",
			`UPDATE catalog_blueprint_versions SET document_json = '{"name":"postgres","x":1}'`,
			false,
		},
		{
			"a version with a title that is empty",
			`UPDATE catalog_blueprint_versions SET document_json = replace(document_json, '"title":"PostgreSQL"', '"title":""')`,
			false,
		},
		{
			"a version of another blueprint",
			`UPDATE catalog_blueprint_versions SET document_json = replace(document_json, '"name":"postgres"', '"name":"redis"')`,
			false,
		},
		{"a version number above the limit", `UPDATE catalog_blueprint_versions SET number = 1001`, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := openInternalTemplated(t)
			seedCatalogBlueprint(t, s)

			if err := checkStagedCatalogBlueprints(t.Context(), s.conn); err != nil {
				t.Fatalf("check of good rows = %v, want nil", err)
			}

			if _, err := s.conn.ExecContext(t.Context(), tc.update); err != nil {
				t.Fatalf("change the row: %v", err)
			}

			switch {
			case tc.draft:
				assertCatalogDraftRefused(t, s.CatalogBlueprints())
			case strings.Contains(tc.update, "number = 1001"):
				assertCatalogVersionRefused(t, s.CatalogBlueprints(), 1001)
			default:
				assertCatalogVersionRefused(t, s.CatalogBlueprints(), 1)
			}

			if err := checkStagedCatalogBlueprints(t.Context(), s.conn); !errors.Is(err, backup.ErrCorrupt) {
				t.Fatalf("check = %v, want backup.ErrCorrupt", err)
			}
		})
	}
}

// TestCheckStagedCatalogBlueprintsRefusesAMissingNamespace proves that a
// blueprint and a version whose namespace the backup does not hold are corrupt.
// An inner join would skip such a row, and the restore would bring it in.
func TestCheckStagedCatalogBlueprintsRefusesAMissingNamespace(t *testing.T) {
	t.Parallel()

	for _, table := range []string{"catalog_blueprints", "catalog_blueprint_versions"} {
		t.Run(table, func(t *testing.T) {
			t.Parallel()

			s := openInternalTemplated(t)
			seedCatalogBlueprint(t, s)

			detachCatalogNamespace(t, s, table)

			err := checkStagedCatalogBlueprints(t.Context(), s.conn)
			if !errors.Is(err, backup.ErrCorrupt) || !strings.Contains(err.Error(), "has no namespace") {
				t.Fatalf("check = %v, want ErrCorrupt for a missing namespace", err)
			}
		})
	}
}

// assertCatalogDraftRefused proves that the store serves no draft of ref.
func assertCatalogDraftRefused(t *testing.T, store CatalogBlueprintStore) {
	t.Helper()

	ref := catalog.BlueprintRef{Namespace: "acme", Name: "postgres"}
	if _, _, err := store.Draft(t.Context(), ref); !errors.Is(err, catalogstore.ErrCorrupt) {
		t.Fatalf("Draft() = %v, want ErrCorrupt", err)
	}
}

// assertCatalogVersionRefused proves that the store serves neither version n
// nor the list of versions.
func assertCatalogVersionRefused(t *testing.T, store CatalogBlueprintStore, n int) {
	t.Helper()

	ref := catalog.BlueprintRef{Namespace: "acme", Name: "postgres"}
	if _, err := store.Version(t.Context(), ref, n); !errors.Is(err, catalogstore.ErrCorrupt) {
		t.Fatalf("Version(%d) = %v, want ErrCorrupt", n, err)
	}

	if _, err := store.ListBlueprints(t.Context(), "acme"); !errors.Is(err, catalogstore.ErrCorrupt) {
		t.Fatalf("ListBlueprints() = %v, want ErrCorrupt", err)
	}
}

// detachCatalogNamespace points every row of table at a namespace that the
// database does not hold. The foreign key refuses this change, so the
// connection that makes it switches the key check off. It is closed before
// the caller reads.
func detachCatalogNamespace(t *testing.T, s *Store, table string) {
	t.Helper()

	pinned, err := s.conn.Conn(t.Context())
	if err != nil {
		t.Fatalf("take a connection: %v", err)
	}

	for _, stmt := range []string{
		`PRAGMA foreign_keys = OFF`,
		`UPDATE ` + table + ` SET namespace_id = 'ns-gone'`,
	} {
		if _, execErr := pinned.ExecContext(t.Context(), stmt); execErr != nil {
			t.Fatalf("%s: %v", stmt, execErr)
		}
	}

	if closeErr := pinned.Close(); closeErr != nil {
		t.Fatalf("release the connection: %v", closeErr)
	}
}
