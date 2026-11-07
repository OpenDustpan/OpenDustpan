package runner

import "errors"

// HandoffReason is the reason code of a refused handoff token. The code is a
// fixed word. It never holds text from a request.
type HandoffReason string

// The reasons that the server gives when it refuses a handoff token.
const (
	// HandoffUnknown is a handoff token that the store does not know.
	HandoffUnknown HandoffReason = "unknown"
	// HandoffExpired is a handoff token after its expiry.
	HandoffExpired HandoffReason = "expired"
	// HandoffUsed is a handoff token that an exchange used already.
	HandoffUsed HandoffReason = "used"
	// HandoffWrongRunner is a request for the claim of another runner.
	HandoffWrongRunner HandoffReason = "wrong-runner"
	// HandoffClaimLost is a handoff token for a claim that is no longer live.
	HandoffClaimLost HandoffReason = "claim-lost"
)

// maxClaimedRunnerLen bounds the runner ID that an unverified request claims.
const maxClaimedRunnerLen = 64

// HandoffRefusal is what the audit entry of a refused handoff token holds:
// the reason code and the runner ID. It holds no token, no hash of a token,
// and no text of the error.
type HandoffRefusal struct {
	Reason HandoffReason
	// Runner is the ID of the runner. For an unverified refusal it is only
	// what the request claimed, and it is empty when the claim does not have
	// the form of a runner ID.
	Runner string
	// Verified is true when the server authenticated the runner with the
	// proof of its key.
	Verified bool
}

// NewVerifiedHandoffRefusal returns the refusal that err stands for, and true,
// for a request that the proof of the key of the runner runnerID
// authenticated. It returns false for an error that is not a refusal of a
// token or a claim, for example an unavailable database: that error is a
// failure, not a refusal.
func NewVerifiedHandoffRefusal(err error, runnerID string) (HandoffRefusal, bool) {
	reason, ok := handoffReasonOf(err)
	if !ok {
		return HandoffRefusal{Reason: "", Runner: "", Verified: false}, false
	}

	return HandoffRefusal{Reason: reason, Runner: runnerID, Verified: true}, true
}

// NewHandoffRefusal is NewVerifiedHandoffRefusal for a request that no proof
// authenticated, for example one with an unknown token. claimedRunner is only
// what the request says. Fail closed: a claim that does not have the form of
// a runner ID is dropped, so a token-like string in the field never reaches an
// audit entry.
func NewHandoffRefusal(err error, claimedRunner string) (HandoffRefusal, bool) {
	reason, ok := handoffReasonOf(err)
	if !ok {
		return HandoffRefusal{Reason: "", Runner: "", Verified: false}, false
	}

	return HandoffRefusal{Reason: reason, Runner: claimedRunnerID(claimedRunner), Verified: false}, true
}

// Event returns the audit event of r.
func (HandoffRefusal) Event() Event {
	return EventHandoffRefused
}

// handoffReasonOf returns the reason code of err, or false when err is not a
// refusal of a token or a claim.
func handoffReasonOf(err error) (HandoffReason, bool) {
	switch {
	case errors.Is(err, ErrTokenUsed):
		return HandoffUsed, true
	case errors.Is(err, ErrTokenExpired):
		return HandoffExpired, true
	case errors.Is(err, ErrWrongRunner):
		return HandoffWrongRunner, true
	case errors.Is(err, ErrClaimLost):
		return HandoffClaimLost, true
	case errors.Is(err, ErrNotFound):
		return HandoffUnknown, true
	default:
		return "", false
	}
}

// claimedRunnerID returns id when it has the form of a runner ID, else "".
func claimedRunnerID(id string) string {
	if len(id) <= len(RunnerIDPrefix) || len(id) > maxClaimedRunnerLen || id[:len(RunnerIDPrefix)] != RunnerIDPrefix {
		return ""
	}

	for _, c := range id[len(RunnerIDPrefix):] {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_':
		default:
			return ""
		}
	}

	return id
}
