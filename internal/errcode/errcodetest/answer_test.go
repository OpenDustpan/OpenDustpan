package errcodetest_test

import (
	"fmt"
	"runtime"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/errcode/errcodetest"
)

// failure runs check on a test double and returns the message of its failure,
// or "" when it passes.
func failure(check func(tb testing.TB)) string {
	tb := &double{TB: nil, message: ""}
	done := make(chan struct{})

	go func() {
		defer close(done)

		check(tb)
	}()

	<-done

	return tb.message
}

// double records the first failure of a check and stops it.
type double struct {
	testing.TB

	message string
}

func (d *double) Helper() {}

func (d *double) Fatalf(format string, args ...any) {
	d.message = fmt.Sprintf(format, args...)

	runtime.Goexit()
}

func TestCheckAnswer(t *testing.T) {
	t.Parallel()

	locked := map[string]any{
		"holder": "user-1", "since": "2026-10-23T09:30:00Z", "target": "acme", "targetKind": "namespace",
		"unit": "acme/web/prod",
	}

	tests := []struct {
		name    string
		shape   errcodetest.Shape
		status  int
		body    string
		want    errcodetest.Want
		request string
		fails   string
	}{
		{
			name: "camelCase, no arguments", shape: errcodetest.ShapeCamelCase, status: 404,
			body:    `{"code":404,"message":"x","errorCode":"unit.not-found"}`,
			want:    errcodetest.Want{Code: errcode.UnitNotFound, Args: nil, RequestBody: nil},
			request: "",
			fails:   "",
		},
		{
			name: "camelCase, arguments", shape: errcodetest.ShapeCamelCase, status: 423,
			body: `{"code":423,"message":"x","errorCode":"lock.locked","errorArgs":` +
				`{"holder":"user-1","since":"2026-10-23T09:30:00Z","target":"acme","targetKind":"namespace","unit":"acme/web/prod"}}`,
			want:    errcodetest.Want{Code: errcode.LockLocked, Args: locked, RequestBody: nil},
			request: "",
			fails:   "",
		},
		{
			name: "camelCase, a quantity", shape: errcodetest.ShapeCamelCase, status: 409,
			body: `{"code":409,"message":"x","errorCode":"maintenance.active-work","errorArgs":` +
				`{"workCount":2,"canceledCount":0}}`,
			want: errcodetest.Want{
				Code:        errcode.MaintenanceActiveWork,
				Args:        map[string]any{"workCount": int64(2), "canceledCount": int64(0)},
				RequestBody: nil,
			},
			request: "",
			fails:   "",
		},
		{
			name: "JSON:API, arguments", shape: errcodetest.ShapeJSONAPI, status: 404,
			body: `{"errors":[{"status":"404","code":"request.idempotency-key-replayed-secret","title":"t",` +
				`"detail":"d","meta":{"args":{"objectID":"obj-1"}}}]}`,
			want: errcodetest.Want{
				Code: errcode.RequestIdempotencyKeyReplayedSecret, Args: map[string]any{"objectID": "obj-1"},
				RequestBody: nil,
			},
			request: "",
			fails:   "",
		},
		{
			name: "wrong code", shape: errcodetest.ShapeCamelCase, status: 404,
			body:    `{"code":404,"message":"x","errorCode":"unit.not-found"}`,
			want:    errcodetest.Want{Code: errcode.NamespaceNotFound, Args: nil, RequestBody: nil},
			fails:   "errorCode",
			request: "",
		},
		{
			name: "no code", shape: errcodetest.ShapeCamelCase, status: 404,
			body:    `{"code":404,"message":"x"}`,
			want:    errcodetest.Want{Code: errcode.UnitNotFound, Args: nil, RequestBody: nil},
			fails:   "errorCode",
			request: "",
		},
		{
			name: "wrong status", shape: errcodetest.ShapeCamelCase, status: 400,
			body:    `{"code":404,"message":"x","errorCode":"unit.not-found"}`,
			want:    errcodetest.Want{Code: errcode.UnitNotFound, Args: nil, RequestBody: nil},
			fails:   "status",
			request: "",
		},
		{
			name: "code outside the catalog", shape: errcodetest.ShapeCamelCase, status: 404,
			body:    `{"code":404,"message":"x","errorCode":"unit.gone"}`,
			want:    errcodetest.Want{Code: "unit.gone", Args: nil, RequestBody: nil},
			fails:   "catalog",
			request: "",
		},
		{
			name:   "an argument the code does not declare",
			shape:  errcodetest.ShapeCamelCase,
			status: 404,
			body:   `{"code":404,"message":"x","errorCode":"unit.not-found","errorArgs":{"name":"web"}}`,
			want: errcodetest.Want{
				Code:        errcode.UnitNotFound,
				Args:        map[string]any{"name": "web"},
				RequestBody: nil,
			},
			fails:   "declare",
			request: "",
		},
		{
			name: "a missing argument", shape: errcodetest.ShapeCamelCase, status: 409,
			body: `{"code":409,"message":"x","errorCode":"maintenance.active-work","errorArgs":{"workCount":2}}`,
			want: errcodetest.Want{
				Code:        errcode.MaintenanceActiveWork,
				Args:        map[string]any{"workCount": int64(2), "canceledCount": int64(0)},
				RequestBody: nil,
			},
			fails:   "arguments",
			request: "",
		},
		{
			name: "another value", shape: errcodetest.ShapeCamelCase, status: 409,
			body: `{"code":409,"message":"x","errorCode":"maintenance.active-work","errorArgs":` +
				`{"workCount":3,"canceledCount":0}}`,
			want: errcodetest.Want{
				Code:        errcode.MaintenanceActiveWork,
				Args:        map[string]any{"workCount": int64(2), "canceledCount": int64(0)},
				RequestBody: nil,
			},
			fails:   "arguments",
			request: "",
		},
		{
			name: "an argument that is a value of the request body", shape: errcodetest.ShapeCamelCase, status: 409,
			body: `{"code":409,"message":"x","errorCode":"request.idempotency-key-replayed-secret",` +
				`"errorArgs":{"objectID":"s3cret"}}`,
			want: errcodetest.Want{
				Code: errcode.RequestIdempotencyKeyReplayedSecret, Args: map[string]any{"objectID": "s3cret"},
				RequestBody: nil,
			},
			request: `{"name":"x","nested":{"token":"s3cret"}}`,
			fails:   "request body",
		},
		{
			name: "JSON:API without a code", shape: errcodetest.ShapeJSONAPI, status: 404,
			body:    `{"errors":[{"status":"404","title":"t"}]}`,
			want:    errcodetest.Want{Code: errcode.UnitNotFound, Args: nil, RequestBody: nil},
			fails:   "code",
			request: "",
		},
		{
			name: "not JSON", shape: errcodetest.ShapeCamelCase, status: 404, body: `nope`,
			want:    errcodetest.Want{Code: errcode.UnitNotFound, Args: nil, RequestBody: nil},
			fails:   "JSON",
			request: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := failure(func(tb testing.TB) {
				tb.Helper()

				tc.want.RequestBody = []byte(tc.request)
				errcodetest.CheckAnswer(tb, tc.shape, tc.status, []byte(tc.body), tc.want)
			})

			if tc.fails == "" && got != "" {
				t.Errorf("failed: %s", got)
			}

			if tc.fails != "" && !strings.Contains(got, tc.fails) {
				t.Errorf("failure = %q, want it to hold %q", got, tc.fails)
			}
		})
	}
}
