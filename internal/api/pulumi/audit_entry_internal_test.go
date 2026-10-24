package pulumi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/requestid"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// TestRequestEntryFillsRequestFacts checks that requestEntry fills the facts
// of the request and the caller in every entry, and takes the rest from the
// detail as given.
func TestRequestEntryFillsRequestFacts(t *testing.T) {
	t.Parallel()

	ns, err := namespace.ParseName("acme")
	if err != nil {
		t.Fatalf("parse namespace: %v", err)
	}

	obj, err := identity.UnitObject(unitTool, ns, "web/prod")
	if err != nil {
		t.Fatalf("unit object: %v", err)
	}

	ctx := requestid.WithContext(t.Context(), "req-1")
	r := httptest.NewRequestWithContext(ctx, http.MethodPost, "/x", http.NoBody)
	r.RemoteAddr = "192.0.2.7:4000"
	r.Header.Set("User-Agent", strings.Repeat("é", audit.MaxUserAgentLen))
	r.Header.Set(channelHeader, string(audit.ChannelAPI))

	var p identity.Principal

	p.Subject, p.TokenID, p.Kind, p.JobID = "ana", "tok-1", identity.KindPerson, "job-1"

	at := time.Unix(1700000000, 0).UTC()
	count := int64(3)

	e := requestEntry(ctx, r, p, obj, auditDetail{
		at: at, action: "unit:decrypt", version: "7", valuePath: "a.b", valueCount: &count,
		kind: audit.KindToolOperation, outcome: audit.OutcomeSuccess,
	})

	checks := []struct {
		name      string
		got, want string
	}{
		{"namespace", e.Namespace.String(), "acme"},
		{"subject", e.Subject, "ana"},
		{"token", e.TokenID, "tok-1"},
		{"object", e.Object, obj.String()},
		{"source ip", e.SourceIP, "192.0.2.7"},
		{"channel", string(e.Channel), string(audit.ChannelAPI)},
		{"request id", e.RequestID, "req-1"},
		{"principal kind", e.PrincipalKind, string(identity.KindPerson)},
		{"job", e.JobID, "job-1"},
		{"action", e.Action, "unit:decrypt"},
		{"version", e.Version, "7"},
		{"value path", e.ValuePath, "a.b"},
		{"kind", string(e.Kind), string(audit.KindToolOperation)},
		{"outcome", string(e.Outcome), string(audit.OutcomeSuccess)},
	}

	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %q, want %q", c.name, c.got, c.want)
		}
	}

	if !e.At.Equal(at) {
		t.Errorf("at = %v, want %v", e.At, at)
	}

	if e.ValueCount == nil || *e.ValueCount != 3 {
		t.Errorf("value count = %v, want 3", e.ValueCount)
	}

	if len(e.UserAgent) > audit.MaxUserAgentLen || !strings.HasPrefix(r.UserAgent(), e.UserAgent) {
		t.Errorf("user agent is %d bytes and not a prefix of the header, want a cut of at most %d bytes",
			len(e.UserAgent), audit.MaxUserAgentLen)
	}
}
