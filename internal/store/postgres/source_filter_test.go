package postgres_test

import (
	"os"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/store/postgres"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// TestListPrincipalsSourceFilter proves that the store filters both the page
// and its total by source, and that a text that is not a source keeps no row
// (fail closed): the source is a placeholder, never a part of the query.
func TestListPrincipalsSourceFilter(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	st := open(t, base)
	ctx := t.Context()
	at := time.Date(2026, time.October, 13, 12, 0, 0, 0, time.UTC)

	local := principal.Principal{
		CreatedAt: at, UpdatedAt: at, DisabledAt: time.Time{}, ID: "prn-source-local",
		Subject: "source-local", DisplayName: "", Kind: principal.KindPerson, Source: principal.SourceLocal,
		Namespace: "", GroupCount: 0, BreakGlass: false,
	}
	if err := st.CreatePrincipal(ctx, local, storetest.ServerChange(at, "principal:"+local.ID)); err != nil {
		t.Fatalf("create the local principal: %v", err)
	}

	wantBuiltin := len(principal.HousekeepingPurposes())

	for _, tc := range []struct {
		name   string
		source principal.Source
		want   int
	}{
		{name: "builtin", source: principal.SourceBuiltin, want: wantBuiltin},
		{name: "local", source: principal.SourceLocal, want: 1},
		{name: "a hostile text", source: "builtin' OR '1'='1", want: 0},
		{name: "a text with a trailing space", source: "builtin ", want: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checkSourceFilter(t, st, tc.source, tc.want)
		})
	}
}

// checkSourceFilter lists the principals of source and checks the rows and
// their total.
func checkSourceFilter(t *testing.T, st *postgres.Store, source principal.Source, want int) {
	t.Helper()

	rows, total, err := st.ListPrincipals(
		t.Context(),
		principal.Query{After: "", Low: "", High: "", Limit: principal.MaxLimit, InstallationOnly: false},
		"", source,
	)
	if err != nil {
		t.Fatalf("list principals of source %q: %v", source, err)
	}

	if len(rows) != want || total != int64(want) {
		t.Fatalf("list principals of source %q = %d rows, total %d, want %d",
			source, len(rows), total, want)
	}

	if source != principal.SourceBuiltin {
		return
	}

	for i := range rows {
		if rows[i].Source != principal.SourceBuiltin {
			t.Fatalf("row %q of the builtin filter has source %q", rows[i].ID, rows[i].Source)
		}
	}
}
