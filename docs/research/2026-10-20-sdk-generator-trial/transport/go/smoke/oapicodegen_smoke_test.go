//go:build oapicodegen

package trial_test

import (
	"net/http"
	"strings"
	"testing"

	dustpan "trial/client"
	"trial/transport"
)

func TestOapiCodegen(t *testing.T) {
	srv, seen := fakeServer(t)
	hc := &http.Client{Transport: &transport.Transport{Token: "t0k"}}
	c, err := dustpan.NewClientWithResponses(srv.URL, dustpan.WithHTTPClient(hc))
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()

	t.Run("get_unit_discriminator_nullable_etag", func(t *testing.T) {
		r, err := c.GetUnitWithResponse(ctx, "ns", "u1")
		if err != nil || r.JSON200 == nil {
			t.Fatalf("err=%v status=%d", err, r.StatusCode())
		}
		v, err := r.JSON200.Details.ValueByDiscriminator()
		p, ok := v.(dustpan.PulumiDetails)
		if err != nil || !ok || p.Stack != "dev" {
			t.Errorf("details: %T %v", v, err)
		}
		if r.JSON200.Lock != nil || r.JSON200.ResourceCount != nil {
			t.Error("null fields not nil")
		}
		if r.Headers200 == nil || r.Headers200.ETag == nil || *r.Headers200.ETag != `"v1"` {
			t.Error("no typed ETag")
		}
		if h, _, _ := seen.get("GET " + v1 + "/namespaces/ns/units/u1"); h.Get("Authorization") != "Bearer t0k" {
			t.Error("no auth header")
		}
	})
	t.Run("unknown_enum_and_discriminator", func(t *testing.T) {
		r, err := c.GetUnitWithResponse(ctx, "ns", "u3")
		if err != nil || r.JSON200 == nil {
			t.Fatalf("err=%v", err)
		}
		if _, err := r.JSON200.Details.ValueByDiscriminator(); err == nil {
			t.Log("unknown discriminator: no error")
		} else {
			t.Logf("unknown discriminator: error at access time only: %v", err)
		}
	})
	t.Run("unknown_enum_only", func(t *testing.T) {
		_, err := c.GetUnitWithResponse(ctx, "ns", "u5")
		if err != nil {
			t.Errorf("a new enum value fails the whole call: %v", err)
		}
	})
	t.Run("error_shape", func(t *testing.T) {
		r, err := c.GetUnitWithResponse(ctx, "ns", "u2")
		if err != nil || r.JSONDefault == nil {
			t.Fatalf("err=%v", err)
		}
		e := r.JSONDefault
		if e.ErrorCode == nil || *e.ErrorCode != "unit.not-found" || e.Code != 404 {
			t.Errorf("error: %+v", e)
		}
		n, err := (*e.ErrorArgs)["limit"].AsErrorErrorArgs1()
		if err != nil || n != 3 {
			t.Errorf("errorArgs limit: %v %v", n, err)
		}
	})
	t.Run("pagination", func(t *testing.T) {
		var ids []string
		var tok *string
		for range 5 {
			r, err := c.ListUnitsWithResponse(ctx, "ns", &dustpan.ListUnitsParams{PageToken: tok})
			if err != nil || r.JSON200 == nil {
				t.Fatalf("err=%v", err)
			}
			for _, u := range r.JSON200.Units {
				ids = append(ids, u.UnitId)
			}
			if r.JSON200.NextPageToken == "" {
				break
			}
			tok = &r.JSON200.NextPageToken
		}
		if strings.Join(ids, ",") != "u1,u4" {
			t.Errorf("ids %v", ids)
		}
	})
	t.Run("merge_patch_null_typed", func(t *testing.T) {
		theme := dustpan.PreferencesThemeDark
		etag := `"p1"`
		_, err := c.UpdatePreferencesWithApplicationMergePatchPlusJSONBodyWithResponse(ctx,
			&dustpan.UpdatePreferencesParams{IfMatch: &etag}, dustpan.Preferences{Theme: &theme, Language: nil})
		if err != nil {
			t.Fatal(err)
		}
		h, b, _ := seen.get("PATCH " + v1 + "/me/preferences")
		if h.Get("Content-Type") != "application/merge-patch+json" || h.Get("If-Match") != etag || h.Get("Idempotency-Key") == "" {
			t.Errorf("headers %v", h)
		}
		if !strings.Contains(b, `"language":null`) {
			t.Errorf("typed body cannot send null: %s", b)
		}
	})
	t.Run("merge_patch_null_raw", func(t *testing.T) {
		_, err := c.UpdatePreferencesWithBodyWithResponse(ctx, &dustpan.UpdatePreferencesParams{},
			"application/merge-patch+json", strings.NewReader(`{"language":null}`))
		if _, b, _ := seen.get("PATCH " + v1 + "/me/preferences"); err != nil || b != `{"language":null}` {
			t.Errorf("raw body %s %v", b, err)
		}
	})
	t.Run("if_match_delete", func(t *testing.T) {
		r, err := c.DeleteBackupWithResponse(ctx, "b1", &dustpan.DeleteBackupParams{IfMatch: `"b1"`})
		if err != nil || r.StatusCode() != 204 {
			t.Errorf("status %d %v", r.StatusCode(), err)
		}
	})
	t.Run("retry_same_idempotency_key", func(t *testing.T) {
		r, err := c.CreateBackupWithResponse(ctx, &dustpan.CreateBackupParams{}, dustpan.BackupCreate{})
		if err != nil || r.JSON202 == nil || r.JSON202.Id != "b9" || r.JSON202.FinishedAt != nil {
			t.Fatalf("err=%v status=%d", err, r.StatusCode())
		}
		if len(seen.keys) < 2 || seen.keys[len(seen.keys)-1] != seen.keys[len(seen.keys)-2] {
			t.Errorf("keys %v", seen.keys)
		}
	})
}
