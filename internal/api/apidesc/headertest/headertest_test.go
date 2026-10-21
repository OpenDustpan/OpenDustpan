package headertest_test

import (
	"net/http"
	"testing"

	"opendustpan.org/opendustpan/internal/api/apidesc"
	"opendustpan.org/opendustpan/internal/api/apidesc/headertest"
)

// operation returns an operation with the method and the flags that a test
// sets.
func operation(id, method string, ifMatchRequired, etag bool) apidesc.Operation {
	return apidesc.Operation{
		ID: id, Method: method, Path: "/api/dustpan/v1/things", Tags: nil, Actions: nil, ActionObjects: nil,
		Public: false, IdempotencyKey: false, IfMatch: false, IfMatchRequired: ifMatchRequired, ETag: etag,
		Bodies: nil,
	}
}

// TestStaleOf proves that the stale tag keeps the form of the tag it came
// from, and that it is never equal to it.
func TestStaleOf(t *testing.T) {
	t.Parallel()

	tests := []struct{ name, etag, want string }{
		{"quoted", `"7"`, `"1007"`},
		{"weak", `W/"7"`, `W/"1007"`},
		{"prefix before the number", `"rev-12"`, `"rev-1012"`},
		{"unquoted", `7`, `1007`},
		{"no number", `"abc"`, `"abc"9999`},
		{"empty", ``, `9999`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := headertest.StaleOf(tt.etag)
			if got != tt.want {
				t.Fatalf("StaleOf(%q) = %q, want %q", tt.etag, got, tt.want)
			}

			if got == tt.etag {
				t.Fatalf("StaleOf(%q) returned the same tag", tt.etag)
			}
		})
	}
}

// TestWritesAndFlagged proves the classification of an operation.
func TestWritesAndFlagged(t *testing.T) {
	t.Parallel()

	for method, want := range map[string]bool{
		http.MethodGet: false, http.MethodHead: false,
		http.MethodPost: true, http.MethodPut: true, http.MethodPatch: true, http.MethodDelete: true,
	} {
		if got := headertest.Writes(operation("x", method, false, false)); got != want {
			t.Errorf("Writes(%s) = %t, want %t", method, got, want)
		}
	}

	if headertest.Flagged(operation("x", http.MethodGet, false, false)) {
		t.Error("Flagged(zero operation) = true")
	}

	if !headertest.Flagged(operation("x", http.MethodGet, false, true)) {
		t.Error("Flagged(ETag) = false")
	}
}

// TestCheckAcceptsAHandlerThatMatchesItsFlags proves the checker passes a
// handler that does what the description says: a versioned write that needs
// If-Match, and a plain write that ignores the header.
func TestCheckAcceptsAHandlerThatMatchesItsFlags(t *testing.T) {
	t.Parallel()

	versioned := func(t *testing.T) headertest.Probe {
		t.Helper()

		return headertest.Probe{
			ETag: `"3"`, Replayed: nil,
			Send: func(headers map[string]string) headertest.Answer {
				switch headers["If-Match"] {
				case "":
					return headertest.Answer{Status: http.StatusPreconditionRequired, Header: http.Header{}, Body: ""}
				case `"3"`:
					return headertest.Answer{
						Status: http.StatusOK, Header: http.Header{"Etag": {`"4"`}}, Body: "",
					}
				default:
					return headertest.Answer{Status: http.StatusPreconditionFailed, Header: http.Header{}, Body: ""}
				}
			},
		}
	}

	plain := func(t *testing.T) headertest.Probe {
		t.Helper()

		return headertest.Probe{
			ETag: "", Replayed: nil,
			Send: func(map[string]string) headertest.Answer {
				return headertest.Answer{Status: http.StatusNoContent, Header: http.Header{}, Body: ""}
			},
		}
	}

	opts := headertest.Options{RefusesKey: false}

	headertest.Check(t, operation("updateThing", http.MethodPatch, true, true), versioned, opts)
	headertest.Check(t, operation("deleteThing", http.MethodDelete, false, false), plain, opts)
}
