package sqlite_test

import (
	"strconv"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/session"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// TestServerAuditConcurrentWritersSQLite starts 16 writers that create
// sign-in provider configs at the same time. Each write must succeed, and
// the chain of @server must check: the store runs each write in an IMMEDIATE
// transaction, which serializes the appends.
func TestServerAuditConcurrentWritersSQLite(t *testing.T) {
	t.Parallel()

	const writers = 16

	s := open(t)

	at := time.Date(2026, time.November, 14, 12, 0, 0, 0, time.UTC)

	errs := make([]error, writers)
	start := make(chan struct{})

	var wg sync.WaitGroup

	for i := range writers {
		wg.Go(func() {
			<-start

			id := "corp-" + strconv.Itoa(i)

			p := session.ProviderConfig{
				CreatedAt:    at,
				UpdatedAt:    at,
				ID:           id,
				Mode:         session.ModeOIDC,
				Issuer:       "https://" + id + ".idp.example",
				DisplayName:  "Corp " + id,
				ClientID:     "dustpan-" + id,
				SealedSecret: []byte("sealed secret"),
				Scopes:       []string{"profile"},
				OAuth: session.OAuthSettings{
					AuthorizationURL: "", TokenURL: "", UserinfoURL: "", SubjectClaim: "",
					NameClaim: "", UsernameClaim: "", EmailClaim: "",
				},
				Version:    1,
				AutoCreate: true,
				Enabled:    false,
			}

			errs[i] = s.CreateProviderConfig(t.Context(), p, storetest.ServerChange(at, "sign-in-provider:"+id))
		})
	}

	close(start)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("writer %d: %v", i, err)
		}
	}

	invalid, err := audit.CheckScopes(t.Context(), s, []namespace.Name{audit.ServerScope})
	if err != nil {
		t.Fatalf("check the server audit scope: %v", err)
	}

	if invalid != 0 {
		t.Fatalf("the server audit chain holds %d breaks", invalid)
	}
}
