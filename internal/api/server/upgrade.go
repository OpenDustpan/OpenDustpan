package server

import (
	"errors"
	"net/http"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/store/schema"
)

// errNoSchemaStore reports an upgrade check on a server with no schema
// store. Every production server wires its store; only tests run without
// one.
var errNoSchemaStore = errors.New("no schema store is configured")

// upgradeCheckResponse answers the upgrade check: the server version, the
// schema version of its database and the names of the applied migrations
// ordered by version. The command compares them with the migrations in its
// own binary and prints the verdicts of ADR 33. KeyEncryptionKey tells
// whether the server runs with a key-encryption key (ADR 41 decision 12). A
// server from before ADR 41 does not answer the field. It writes nothing.
type upgradeCheckResponse struct {
	Version           string   `json:"version"`
	SchemaVersion     int64    `json:"schemaVersion"`
	AppliedMigrations []string `json:"appliedMigrations"`
	KeyEncryptionKey  bool     `json:"keyEncryptionKey"`
}

// serveUpgradeCheck answers the server version, the schema version of its
// database and the names of the applied migrations. It needs the action
// server:observe on the installation (@server): it reads installation state
// and writes nothing.
func (h *Handler) serveUpgradeCheck(
	w http.ResponseWriter, r *http.Request, _ identity.Principal, _ identity.Object,
) {
	ctx := r.Context()

	if h.schema == nil {
		h.writeError(ctx, w, errNoSchemaStore)

		return
	}

	applied, err := h.schema.AppliedMigrations(ctx)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	version := h.version
	if version == "" {
		version = "dev"
	}

	h.writeJSON(ctx, w, http.StatusOK, upgradeCheckResponse{
		Version:           version,
		SchemaVersion:     schema.DatabaseVersion(appliedVersions(applied)),
		AppliedMigrations: appliedNames(applied),
		KeyEncryptionKey:  h.keyed,
	})
}

// appliedVersions returns the versions of the applied migrations.
func appliedVersions(applied []schema.AppliedMigration) []int64 {
	versions := make([]int64, 0, len(applied))
	for _, migration := range applied {
		versions = append(versions, migration.Version)
	}

	return versions
}

// appliedNames returns the file names of the applied migrations in order.
func appliedNames(applied []schema.AppliedMigration) []string {
	names := make([]string, 0, len(applied))
	for _, migration := range applied {
		names = append(names, migration.Name)
	}

	return names
}
