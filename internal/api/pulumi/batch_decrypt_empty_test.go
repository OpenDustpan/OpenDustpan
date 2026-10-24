package pulumi_test

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"

	"opendustpan.org/opendustpan/pkg/identity"
)

// TestBatchDecryptOfNoValues proves that a batch decrypt with an empty or a
// null list of ciphertexts answers 200 with no plaintext and writes no audit
// entry, also for a stack that has no secret yet. Every other body that the
// route refused before is still refused.
func TestBatchDecryptOfNoValues(t *testing.T) {
	t.Parallel()

	const target = "/api/stacks/acme/web/other/batch-decrypt"

	for _, tc := range []struct {
		name   string
		body   string
		gzip   bool
		status int
		want   string
	}{
		{name: "empty list", body: `{"ciphertexts":[]}`, gzip: false, status: http.StatusOK, want: `{"plaintexts":{}}`},
		{name: "null list", body: `{"ciphertexts":null}`, gzip: false, status: http.StatusOK, want: `{"plaintexts":{}}`},
		{name: "null list, gzip", body: `{"ciphertexts":null}`, gzip: true, status: http.StatusOK, want: `{"plaintexts":{}}`},
		{name: "no member", body: `{}`, gzip: false, status: http.StatusBadRequest, want: "invalid JSON body"},
		{name: "not JSON", body: `{`, gzip: false, status: http.StatusBadRequest, want: "invalid JSON body"},
		{
			name: "two documents", body: `{"ciphertexts":[]}{}`, gzip: false, status: http.StatusBadRequest,
			want: "invalid JSON body",
		},
		{
			name: "not base64", body: `{"ciphertexts":["!"]}`, gzip: false, status: http.StatusBadRequest,
			want: "invalid JSON body",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			entries := &memAuditStore{entries: nil, err: nil, mu: sync.Mutex{}}
			f := buildSecretFixture(t, allowAll(), 0,
				fixtureAudit{store: entries, onFail: nil, logger: slog.New(slog.DiscardHandler)})

			send := f.plain
			if tc.gzip {
				send = f.gzipped
			}

			status, body := send(t, http.MethodPost, target, tc.body)
			if status != tc.status || !strings.Contains(body, tc.want) {
				t.Fatalf("POST %s answers %d %s, want %d with %s", tc.body, status, body, tc.status, tc.want)
			}

			if n := len(entries.entries); n != 0 {
				t.Fatalf("POST %s wrote %d audit entries, want none", tc.body, n)
			}
		})
	}
}

// TestBatchDecryptOfNoValuesNeedsTheAction proves that an empty batch
// decrypt still needs unit:decrypt: authorization runs before the empty
// check.
func TestBatchDecryptOfNoValuesNeedsTheAction(t *testing.T) {
	t.Parallel()

	f := newSecretFixture(t, selectiveAuthorizer{deny: map[identity.Action]bool{identity.ActionUnitDecrypt: true}}, 0)

	status, body := f.plain(t, http.MethodPost, "/api/stacks/acme/web/other/batch-decrypt", `{"ciphertexts":[]}`)
	if status != http.StatusForbidden {
		t.Fatalf("POST answers %d %s, want 403", status, body)
	}

	var doc struct {
		Plaintexts json.RawMessage `json:"plaintexts"`
	}

	if err := json.Unmarshal([]byte(body), &doc); err == nil && doc.Plaintexts != nil {
		t.Fatalf("a refused call answers plaintexts: %s", body)
	}
}
