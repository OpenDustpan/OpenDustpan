package runnerpools

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"time"

	"opendustpan.org/opendustpan/internal/clientip"
	"opendustpan.org/opendustpan/internal/requestid"
	"opendustpan.org/opendustpan/internal/sessionauth"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/runner"
)

// changeEvent is the key of the change that names the event of an audit entry.
const changeEvent = "event"

// entry returns the audit entry of a change in ns by subject.
func (h *Handler) entry(
	r *http.Request, ns namespace.Name, subject string, rule runner.AuditRule, at time.Time,
) audit.Entry {
	return audit.Entry{
		At: at, Namespace: ns, Subject: subject, IDPSubject: "", TokenID: "", Action: string(rule.Action),
		Object: identity.NamespaceObject(ns).String(), Version: "", ValuePath: "", ValueCount: nil,
		Changes: nil, SourceIP: clientip.String(r), UserAgent: audit.TruncateUserAgent(r.UserAgent()),
		Channel: sessionauth.AuditChannel(r, channelHeader), Kind: rule.Kind, Outcome: audit.OutcomeSuccess,
		RequestID: requestid.FromContext(r.Context()), ObjectID: "", PrevHash: "", Hash: "", Sequence: 0,
		FenceVersion: 0, PrincipalKind: "", JobID: "", Reason: "",
		Origin: nil,
	}
}

// newSecret returns a secret that starts with prefix and holds 256 random
// bits as hex.
func newSecret(prefix string) (string, error) {
	var b [32]byte

	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("read random bytes: %w", err)
	}

	return prefix + hex.EncodeToString(b[:]), nil
}

// sha256Hex returns the hex SHA-256 of s.
func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))

	return hex.EncodeToString(sum[:])
}
