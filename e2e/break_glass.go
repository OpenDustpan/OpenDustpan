//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
)

// breakGlassActor is the subject of the audit entries of SeedBreakGlass.
const breakGlassActor = "e2e-break-glass-seed"

// SeedBreakGlass stores one enabled break-glass person for each subject and
// returns the IDs, in the order of the subjects. No API route makes a
// break-glass account, so this is a direct store write. The binary server owns
// its database, so the write runs while the binary is stopped, on a handle that
// is closed before the binary starts again. The in-process server writes
// through its own store.
func (e *Env) SeedBreakGlass(ctx context.Context, tb testing.TB, subjects ...string) []string {
	tb.Helper()

	ids := make([]string, 0, len(subjects))
	for _, subject := range subjects {
		ids = append(ids, "prn-e2e-bg-"+subject)
	}

	write := func(st namespace.Store) error { return seedBreakGlass(ctx, st, subjects, ids) }

	if e.binPath == "" {
		if err := write(e.inprocess); err != nil {
			tb.Fatalf("make the break-glass accounts: %v", err)
		}

		return ids
	}

	e.StopServer(ctx, tb)
	e.writeStopped(ctx, tb, write)
	e.StartServer(ctx, tb)

	return ids
}

// seedBreakGlass stores the break-glass persons of SeedBreakGlass.
func seedBreakGlass(ctx context.Context, st namespace.Store, subjects, ids []string) error {
	rows, ok := st.(principal.Store)
	if !ok {
		return errNoAccessRows
	}

	at := time.Now().UTC().Truncate(time.Millisecond)

	for i, subject := range subjects {
		p := principal.Principal{
			CreatedAt: at, UpdatedAt: at, DisabledAt: time.Time{}, ID: ids[i],
			Subject: "break-glass:" + subject, DisplayName: subject, Kind: principal.KindPerson,
			Source: principal.SourceLocal, Namespace: "", GroupCount: 0, BreakGlass: true,
		}

		e := audit.Entry{
			At: at, Namespace: audit.ServerScope, Subject: breakGlassActor, IDPSubject: "", TokenID: "",
			Action: "server:manage-principals", Object: principal.Ref(p.ID), Version: "", ValuePath: "",
			ValueCount: nil, Changes: nil, SourceIP: "192.0.2.1", UserAgent: breakGlassActor,
			Channel: audit.ChannelAPI, Kind: audit.KindChange, Outcome: audit.OutcomeSuccess, RequestID: "",
			ObjectID: "", PrevHash: "", Hash: "", Sequence: 0, FenceVersion: 0, Reason: "", PrincipalKind: "",
			JobID: "",
		}

		if err := rows.CreatePrincipal(ctx, p, e); err != nil {
			return fmt.Errorf("create the break-glass person %q: %w", subject, err)
		}
	}

	return nil
}
