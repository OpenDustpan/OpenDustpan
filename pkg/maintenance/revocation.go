package maintenance

import (
	"errors"
	"fmt"
	"strconv"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
)

// The kinds of access-reducing write that a store journals while the
// installation is in maintenance mode (see Store.SetState). A restore
// applies the journal again after the swap, so a revocation made during
// maintenance mode survives a restore of older access data. Each kind only
// ends, revokes or disables.
const (
	// RevokedSession is the end of a session. The target is the session ID.
	RevokedSession = "session"
	// RevokedToken is the revocation of an access token or a service token.
	// The target is the token ID.
	RevokedToken = "token"
	// RevokedPrincipal is the disable of a principal. The target is the
	// principal ID.
	RevokedPrincipal = "principal"
	// RevokedTempAdmin is the expiry of a temporary administrator. The
	// target is the principal ID. The replay ends the record, disables the
	// principal, revokes its grants and tokens and ends its memberships.
	RevokedTempAdmin = "temp-admin"
	// RevokedGrant is the revocation of a grant. The target is the grant ID.
	RevokedGrant = "grant"
	// RevokedJoinToken is the revocation of a join token of a runner pool.
	// The target is the join token ID.
	RevokedJoinToken = "join-token"
	// RevokedProvider is the disable of an identity provider, which also
	// ends its sessions. The target is the provider ID.
	RevokedProvider = "provider"
)

// ActionReplayRevocation is the action of the audit entry that a restore
// writes for each revocation that it applies again. The restore replaces the
// audit log with the log of the backup, so the entry of the original
// revocation is gone from it.
const ActionReplayRevocation = "maintenance:replay-revocation"

// ErrInvalidRevocation reports a journal row that a replay cannot apply: an
// unknown kind, or a target that is not an ID.
var ErrInvalidRevocation = errors.New("invalid journaled revocation")

// maxTargetLen bounds the length of the target of a revocation. IDs of the
// installation are far shorter.
const maxTargetLen = 255

// Revocation is one row of the journal of the writes that only reduce access
// and that ran in maintenance mode.
type Revocation struct {
	// ID is the order of the row in the journal.
	ID int64
	// Kind is one of the Revoked constants.
	Kind string
	// Target is the ID of the session, token, principal, grant, join token or
	// provider that the write ended.
	Target string
	// EndReason is the end reason of a session. It is empty for other kinds.
	EndReason string
	// Actor is the subject that made the write.
	Actor string
	// Object is the object of the audit entry of the write.
	Object string
	// At is the time of the write.
	At time.Time
}

// Validate returns ErrInvalidRevocation unless the kind is known and the
// target is an ID. Defense in depth: the CHECK constraint of the journal
// table limits the kind and the store writes the row from a typed value, so a
// hostile row needs a write to the database itself. The replay still checks
// each row as if those layers failed, and it uses the target only as a bound
// parameter.
func (r Revocation) Validate() error {
	switch r.Kind {
	case RevokedSession, RevokedToken, RevokedPrincipal, RevokedTempAdmin, RevokedGrant, RevokedJoinToken,
		RevokedProvider:
	default:
		return fmt.Errorf("%w: unknown kind %q", ErrInvalidRevocation, r.Kind)
	}

	if !validTarget(r.Target) {
		return fmt.Errorf("%w: target %q is not an ID", ErrInvalidRevocation, r.Target)
	}

	if r.Actor == "" || r.Object == "" {
		return fmt.Errorf("%w: row %d has no actor or object", ErrInvalidRevocation, r.ID)
	}

	return nil
}

// validTarget reports whether s is an ID: 1 to maxTargetLen characters of
// letters, digits and the separators "-", "_" and "." that the IDs of the
// installation use.
func validTarget(s string) bool {
	if s == "" || len(s) > maxTargetLen {
		return false
	}

	for _, c := range []byte(s) {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case c == '-', c == '_', c == '.':
		default:
			return false
		}
	}

	return true
}

// ReplayEntry returns the audit entry of the replay of r in the scope
// @server: by the subject of the original write, on its object, at its time.
// When invalid is not nil, the row failed Validate and the replay applied
// nothing: the entry has the outcome denied and names the journal row, never
// the content of the row.
func (r Revocation) ReplayEntry(invalid error) audit.Entry {
	e := audit.Entry{
		At:            r.At,
		Namespace:     audit.ServerScope,
		Subject:       r.Actor,
		IDPSubject:    "",
		TokenID:       "",
		Action:        ActionReplayRevocation,
		Object:        r.Object,
		Version:       "",
		ValuePath:     "/maintenance/revocations/" + r.Kind,
		ValueCount:    nil,
		Changes:       nil,
		SourceIP:      "",
		UserAgent:     "",
		Channel:       audit.ChannelAPI,
		Kind:          audit.KindChange,
		Outcome:       audit.OutcomeSuccess,
		RequestID:     "",
		ObjectID:      "",
		PrevHash:      "",
		Hash:          "",
		Sequence:      0,
		FenceVersion:  0,
		PrincipalKind: "",
		JobID:         "",
		Reason:        "",
	}

	if invalid == nil {
		return e
	}

	// A hostile row can hold any text. The denial names the row by its
	// number and the subject is the fixed name of this component.
	e.Outcome = audit.OutcomeDenied
	e.Subject = maintenanceSubject
	e.Object = "maintenance-revocation:" + strconv.FormatInt(r.ID, 10)
	e.ValuePath = "/maintenance/revocations/invalid"

	return e
}
