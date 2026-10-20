//go:build openapigen

package trial_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	dustpan "trial/client"
	"trial/transport"
)

func TestOpenAPIGenerator(t *testing.T) {
	srv, seen := fakeServer(t)
	cfg := dustpan.NewConfiguration()
	cfg.Servers = dustpan.ServerConfigurations{{URL: srv.URL}}
	cfg.HTTPClient = &http.Client{Transport: &transport.Transport{}}
	c := dustpan.NewAPIClient(cfg)
	ctx := context.WithValue(t.Context(), dustpan.ContextAccessToken, "t0k")

	t.Run("get_unit_discriminator_nullable_etag", func(t *testing.T) {
		u, r, err := c.UnitsAPI.GetUnit(ctx, "ns", "u1").Execute()
		if err != nil {
			t.Fatalf("an unknown answer field fails the call: %v", err)
		}
		if u.Details.PulumiDetails == nil || u.Details.PulumiDetails.Stack != "dev" {
			t.Errorf("details %+v", u.Details)
		}
		if u.Lock.Get() != nil || u.ResourceCount.Get() != nil {
			t.Error("null fields not nil")
		}
		if r.Header.Get("ETag") != `"v1"` {
			t.Error("no ETag on the raw answer")
		}
		if h, _, _ := seen.get("GET " + v1 + "/namespaces/ns/units/u1"); h.Get("Authorization") != "Bearer t0k" {
			t.Error("no auth header")
		}
	})
	t.Run("unknown_enum_and_discriminator", func(t *testing.T) {
		if _, _, err := c.UnitsAPI.GetUnit(ctx, "ns", "u3").Execute(); err != nil {
			t.Errorf("a new enum value fails the whole call: %v", err)
		}
	})
	t.Run("unknown_enum_only", func(t *testing.T) {
		_, _, err := c.UnitsAPI.GetUnit(ctx, "ns", "u5").Execute()
		if err != nil {
			t.Errorf("a new enum value fails the whole call: %v", err)
		}
	})
	t.Run("error_shape", func(t *testing.T) {
		_, _, err := c.UnitsAPI.GetUnit(ctx, "ns", "u2").Execute()
		var e *dustpan.GenericOpenAPIError
		if !errors.As(err, &e) {
			t.Fatalf("err %T %v", err, err)
		}
		m, ok := e.Model().(dustpan.Error)
		if !ok || m.GetErrorCode() != "unit.not-found" {
			t.Errorf("model %T %+v", e.Model(), e.Model())
		}
		if a := m.GetErrorArgs()["limit"]; a.Int64 == nil || *a.Int64 != 3 {
			t.Errorf("errorArgs limit %+v", a)
		}
	})
	t.Run("pagination", func(t *testing.T) {
		var ids []string
		tok := ""
		for range 5 {
			req := c.UnitsAPI.ListUnits(ctx, "ns")
			if tok != "" {
				req = req.PageToken(tok)
			}
			l, _, err := req.Execute()
			if err != nil {
				t.Fatalf("err %v", err)
			}
			for _, u := range l.Units {
				ids = append(ids, u.UnitId)
			}
			if tok = l.NextPageToken; tok == "" {
				break
			}
		}
		if strings.Join(ids, ",") != "u1,u4" {
			t.Errorf("ids %v", ids)
		}
	})
	t.Run("merge_patch_null_typed", func(t *testing.T) {
		p := dustpan.NewPreferences()
		p.SetTheme("dark")
		p.SetLanguageNil()
		etag := `"p1"`
		_, _, err := c.MeAPI.UpdatePreferences(ctx).Preferences(*p).IfMatch(etag).Execute()
		h, b, _ := seen.get("PATCH " + v1 + "/me/preferences")
		if h.Get("Content-Type") != "application/merge-patch+json" || h.Get("If-Match") != etag || h.Get("Idempotency-Key") == "" {
			t.Errorf("headers %v", h)
		}
		if !strings.Contains(b, `"language":null`) {
			t.Errorf("typed body cannot send null: %s", b)
		}
		if err != nil {
			t.Errorf("answer: %v", err)
		}
	})
	t.Run("if_match_delete", func(t *testing.T) {
		if _, err := c.BackupsAPI.DeleteBackup(ctx, "b1").IfMatch(`"b1"`).Execute(); err != nil {
			t.Error(err)
		}
	})
	t.Run("retry_same_idempotency_key", func(t *testing.T) {
		b, _, err := c.BackupsAPI.CreateBackup(ctx).Execute()
		if err != nil || b.Id != "b9" || b.FinishedAt.Get() != nil {
			t.Fatalf("err=%v", err)
		}
		if len(seen.keys) < 2 || seen.keys[len(seen.keys)-1] != seen.keys[len(seen.keys)-2] {
			t.Errorf("keys %v", seen.keys)
		}
	})
}
