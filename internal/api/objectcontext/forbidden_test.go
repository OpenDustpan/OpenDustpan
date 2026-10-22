package objectcontext_test

import (
	"net/http"
	"testing"

	"opendustpan.org/opendustpan/internal/errcode/errcodetest"
)

// TestForbiddenCodes proves that each cause of a 403 keeps its message and
// status and adds its machine code, on the first write route.
func TestForbiddenCodes(t *testing.T) {
	t.Parallel()

	var write *routeCase

	cases := casesOf(t)
	for i := range cases {
		if cases[i].rt.Method == http.MethodPatch {
			write = &cases[i]

			break
		}
	}

	if write == nil {
		t.Fatal("no write route")
	}

	for _, cause := range errcodetest.Causes(t) {
		t.Run(cause.Name, func(t *testing.T) {
			t.Parallel()

			h := newHandler(t, errcodetest.ReadsThenRefuses{Err: cause.Err}, newFakeStore())
			rec := serve(
				t,
				h,
				request{method: write.rt.Method, path: write.path, token: testToken, body: write.body},
			)

			errcodetest.Check(t, rec.Code, rec.Body.Bytes(), cause)
		})
	}
}
