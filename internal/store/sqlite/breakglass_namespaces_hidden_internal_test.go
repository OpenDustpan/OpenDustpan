package sqlite

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/principal"
)

// TestBreakGlassNamespaceScopesFailsClosedWhenPrincipalsIsHidden proves that
// the namespace scopes of the restore check return ErrBreakGlassUnavailable
// when the principals table cannot be read. The restore check uses these
// scopes before it counts each namespace, so an unreadable table must stop the
// check and must not be read as no namespace holding a break-glass account.
func TestBreakGlassNamespaceScopesFailsClosedWhenPrincipalsIsHidden(t *testing.T) {
	t.Parallel()

	s := openSeededKeepStore(t)

	_, err := s.conn.ExecContext(t.Context(), `ALTER TABLE principals RENAME TO principals_hidden`)
	if err != nil {
		t.Fatalf("hide the principals table: %v", err)
	}

	tx, err := s.conn.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatalf("begin the transaction: %v", err)
	}

	defer func() {
		if rbErr := tx.Rollback(); rbErr != nil {
			t.Errorf("roll back the read transaction: %v", rbErr)
		}
	}()

	scopes, err := breakGlassNamespaceScopes(t.Context(), tx, breakGlassSchemaMain)
	if !errors.Is(err, principal.ErrBreakGlassUnavailable) {
		t.Fatalf("namespace scopes with a hidden principals table = %v, %v; want ErrBreakGlassUnavailable",
			scopes, err)
	}
}
