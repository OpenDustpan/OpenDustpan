//go:build ogen

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

type token struct{}

func (token) BearerAuth(context.Context, dustpan.OperationName) (dustpan.BearerAuth, error) {
	return dustpan.BearerAuth{Token: "t0k"}, nil
}

func TestOgen(t *testing.T) {
	srv, seen := fakeServer(t)
	// The token comes from the SecuritySource of ogen here. The transport
	// could set it too; it must not set it twice.
	hc := &http.Client{Transport: &transport.Transport{}}
	c, err := dustpan.NewClient(srv.URL, token{}, dustpan.WithClient(hc))
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()

	t.Run("get_unit_discriminator_nullable_etag", func(t *testing.T) {
		r, err := c.GetUnit(ctx, dustpan.GetUnitParams{Namespace: "ns", UnitID: "u1"})
		if err != nil {
			t.Fatal(err)
		}
		u := r.Response
		if u.Details.Type != dustpan.PulumiDetailsUnitDetails || u.Details.PulumiDetails.Stack != "dev" {
			t.Errorf("details %+v", u.Details)
		}
		if !u.Lock.IsNull() || !u.ResourceCount.IsNull() {
			t.Error("null fields not null")
		}
		if v, ok := r.ETag.Get(); !ok || v != `"v1"` {
			t.Error("no typed ETag")
		}
		if h, _, _ := seen.get("GET " + v1 + "/namespaces/ns/units/u1"); h.Get("Authorization") != "Bearer t0k" {
			t.Error("no auth header")
		}
	})
	t.Run("unknown_enum_and_discriminator", func(t *testing.T) {
		_, err := c.GetUnit(ctx, dustpan.GetUnitParams{Namespace: "ns", UnitID: "u3"})
		if err != nil {
			t.Errorf("a new enum value fails the whole call: %v", err)
		}
	})
	t.Run("unknown_enum_only", func(t *testing.T) {
		_, err := c.GetUnit(ctx, dustpan.GetUnitParams{Namespace: "ns", UnitID: "u5"})
		if err != nil {
			t.Errorf("a new enum value fails the whole call: %v", err)
		}
	})
	t.Run("error_shape", func(t *testing.T) {
		_, err := c.GetUnit(ctx, dustpan.GetUnitParams{Namespace: "ns", UnitID: "u2"})
		var e *dustpan.ErrorStatusCode
		if !errors.As(err, &e) {
			t.Fatalf("err %T %v", err, err)
		}
		if e.StatusCode != 404 || e.Response.ErrorCode.Or("") != "unit.not-found" {
			t.Errorf("error %+v", e)
		}
		if a := e.Response.ErrorArgs.Or(nil)["limit"]; !a.IsInt64() || a.Int64 != 3 {
			t.Errorf("errorArgs limit %+v", a)
		}
	})
	t.Run("pagination", func(t *testing.T) {
		var ids []string
		p := dustpan.ListUnitsParams{Namespace: "ns"}
		for range 5 {
			r, err := c.ListUnits(ctx, p)
			if err != nil {
				t.Fatal(err)
			}
			for _, u := range r.Units {
				ids = append(ids, u.UnitId)
			}
			if r.NextPageToken == "" {
				break
			}
			p.PageToken = dustpan.NewOptString(r.NextPageToken)
		}
		if strings.Join(ids, ",") != "u1,u4" {
			t.Errorf("ids %v", ids)
		}
	})
	t.Run("merge_patch_null_typed", func(t *testing.T) {
		var lang dustpan.OptNilString
		lang.SetToNull()
		etag := `"p1"`
		_, err := c.UpdatePreferences(ctx, &dustpan.Preferences{Theme: dustpan.NewOptPreferencesTheme(dustpan.PreferencesThemeDark), Language: lang},
			dustpan.UpdatePreferencesParams{IfMatch: dustpan.NewOptString(etag)})
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
	t.Run("if_match_delete", func(t *testing.T) {
		if err := c.DeleteBackup(ctx, dustpan.DeleteBackupParams{ID: "b1", IfMatch: `"b1"`}); err != nil {
			t.Error(err)
		}
	})
	t.Run("retry_same_idempotency_key", func(t *testing.T) {
		r, err := c.CreateBackup(ctx, dustpan.OptBackupCreate{}, dustpan.CreateBackupParams{})
		if err != nil || r.Response.ID != "b9" || !r.Response.FinishedAt.IsNull() {
			t.Fatalf("err=%v", err)
		}
		if len(seen.keys) < 2 || seen.keys[len(seen.keys)-1] != seen.keys[len(seen.keys)-2] {
			t.Errorf("keys %v", seen.keys)
		}
	})
}
