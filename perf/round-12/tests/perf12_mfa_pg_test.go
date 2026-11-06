package mfasvc_test

import (
	"context"
	"net/url"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/internal/mfasvc"
	"opendustpan.org/opendustpan/internal/webauthntest"
	"opendustpan.org/opendustpan/pkg/mfa/gowebauthn"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/store/postgres"
)

type countTracer struct {
	on  atomic.Bool
	mu  sync.Mutex
	sql []string
}

func (c *countTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, d pgx.TraceQueryStartData) context.Context {
	if c.on.Load() {
		c.mu.Lock()
		c.sql = append(c.sql, strings.Join(strings.Fields(d.SQL), " "))
		c.mu.Unlock()
	}

	return ctx
}

func (c *countTracer) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func (c *countTracer) take() []string {
	c.mu.Lock()
	defer c.mu.Unlock()

	out := c.sql
	c.sql = nil

	return out
}

func TestPerf12PasskeyRoundTripsPostgres(t *testing.T) {
	dbURL := os.Getenv("DUSTPAN_TEST_POSTGRES_URL")
	if dbURL == "" {
		t.Skip("no postgres")
	}

	conn, err := pgx.Connect(t.Context(), dbURL)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(t.Context())

	for _, s := range []string{"perf12_mfa", "perf12_mfa_control"} {
		if _, err := conn.Exec(t.Context(), "DROP SCHEMA IF EXISTS "+s+" CASCADE"); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := conn.Exec(t.Context(), "CREATE SCHEMA perf12_mfa"); err != nil {
		t.Fatal(err)
	}

	u, _ := url.Parse(dbURL)
	q := u.Query()
	q.Set("search_path", "perf12_mfa")
	u.RawQuery = q.Encode()

	tr := &countTracer{}

	st, err := postgres.Open(t.Context(), u.String(), postgres.WithQueryTracer(tr))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)

	p := principal.Principal{
		CreatedAt: time.Now(), UpdatedAt: time.Now(), ID: alice, Subject: alice, DisplayName: alice,
		Kind: principal.KindPerson, Source: principal.SourceLocal,
	}
	if err := st.CreatePrincipal(t.Context(), p, base(time.Now(), principal.Ref(alice))); err != nil {
		t.Fatal(err)
	}

	rp, err := gowebauthn.New(externalURL)
	if err != nil {
		t.Fatal(err)
	}

	svc, err := mfasvc.New(mfasvc.Config{Store: st, RP: rp, Audit: st, Now: nil, Log: nil})
	if err != nil {
		t.Fatal(err)
	}

	auth := webauthntest.New(rpID, externalURL)
	one := func(measure bool) (nb, nf int, durB, durF time.Duration, ab, af uint64) {
		a := actor(alice, "ses-"+alice)

		var m0, m1, m2, m3 runtime.MemStats

		tr.on.Store(measure)
		runtime.ReadMemStats(&m0)

		s0 := time.Now()
		options, err := svc.BeginRegistration(t.Context(), a, personOf(alice))
		durB = time.Since(s0)

		runtime.ReadMemStats(&m1)
		tr.on.Store(false)

		if err != nil {
			t.Fatal(err)
		}

		nb = len(tr.take())

		c, _ := webauthntest.ChallengeOf(options)
		cred, _ := webauthntest.NewCredential([]byte(alice), false)
		resp, err := auth.Register(cred, webauthntest.For(c))

		if err != nil {
			t.Fatal(err)
		}

		tr.on.Store(measure)
		runtime.ReadMemStats(&m2)

		s0 = time.Now()
		_, err = svc.FinishRegistration(t.Context(), a, personOf(alice), "bench", resp)
		durF = time.Since(s0)

		runtime.ReadMemStats(&m3)
		tr.on.Store(false)

		if err != nil {
			t.Fatal(err)
		}

		sql := tr.take()
		nf = len(sql)

		if measure && os.Getenv("PERF12_SQL") != "" {
			for _, s := range sql {
				t.Log("finish sql:", s[:min(len(s), 90)])
			}
		}

		return nb, nf, durB, durF, m1.Mallocs - m0.Mallocs, m3.Mallocs - m2.Mallocs
	}

	for range 3 {
		one(false)
		// the service removes nothing: delete passkeys so the person stays under the limit
		if _, err := conn.Exec(t.Context(), "DELETE FROM perf12_mfa.passkeys"); err != nil {
			t.Log("cleanup:", err)
		}
	}

	for i := range 5 {
		nb, nf, db, df, ab, af := one(true)
		t.Logf("PERF12 passkey-service-pg run=%d begin_trips=%d finish_trips=%d begin_ms=%.2f finish_ms=%.2f begin_allocs=%d finish_allocs=%d",
			i, nb, nf, float64(db.Microseconds())/1000, float64(df.Microseconds())/1000, ab, af)

		_, _ = conn.Exec(t.Context(), "DELETE FROM perf12_mfa.passkeys")
	}
}
