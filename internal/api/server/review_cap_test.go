package server_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/errcode/errcodetest"
	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/setting"
)

// errCapCollectionAbsent is the answer of the fake for a collection that is
// not payments.
var errCapCollectionAbsent = errors.New("collection not found")

// capCollections is the collection port of the export of the collection
// payments of acme, with count units. No other collection exists.
type capCollections struct{ count int }

func (capCollections) CollectionOf(context.Context, identity.Object) (string, bool, error) {
	return "", false, nil
}

func (c capCollections) CollectionID(_ context.Context, obj identity.Object) (string, bool, error) {
	if obj.CollectionName() == "payments" {
		return "col-payments", true, nil
	}

	return "", false, nil
}

func (c capCollections) CollectionUnitIDs(_ context.Context, col identity.Object) ([]string, error) {
	if col.CollectionName() != "payments" {
		return nil, errCapCollectionAbsent
	}

	ids := make([]string, 0, c.count)

	for i := range c.count {
		ids = append(ids, "stk-unit-"+strconv.Itoa(i))
	}

	return ids, nil
}

// capSettings answers the value of the cap of the export of a collection.
type capSettings struct{ value string }

func (s capSettings) Effective(context.Context, setting.Scope, string) (setting.Item, error) {
	return setting.Effective(access.SettingDeclarations()[0], &s.value, nil), nil
}

// TestReviewOfACollectionOverTheCap proves that the export of a collection
// over its cap answers 422 with the code of the refusal and its arguments,
// and that the message names the setting key.
func TestReviewOfACollectionOverTheCap(t *testing.T) {
	t.Parallel()

	f := newAccessFixturePorts(t, accessPorts{
		collections: capCollections{count: 3}, settings: capSettings{value: "2"},
	})

	rec := f.asToken(t, "alice", http.MethodGet,
		"/api/server/namespaces/acme/collections/payments/access-review", nil)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusUnprocessableEntity, rec.Body.String())
	}

	errcodetest.CheckAnswer(t, errcodetest.ShapeCamelCase, http.StatusUnprocessableEntity, rec.Body.Bytes(),
		errcodetest.Want{
			Code: errcode.AccessCollectionUnitCap,
			Args: map[string]any{
				"count": int64(3), "cap": int64(2), "key": access.SettingCollectionUnitCap,
			},
			RequestBody: nil,
		})

	var body struct {
		Message string `json:"message"`
	}

	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %s: %v", rec.Body.String(), err)
	}

	if !strings.Contains(body.Message, access.SettingCollectionUnitCap) {
		t.Fatalf("message = %q, want the setting key %q", body.Message, access.SettingCollectionUnitCap)
	}
}
