package backup

import (
	"slices"
	"strconv"
	"strings"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/history"
)

// ActionDroppedRunnerCredentials is the action of the audit entry that a
// restore writes for the live runner credentials that it does not keep: the
// handoff tokens and the job sessions whose runner the restored data does not
// hold. The restore drops those rows in its swap transaction, so no later
// read of the restored data can name them.
const ActionDroppedRunnerCredentials = "restore:drop-runner-credentials"

// DroppedRunnerCredentials is what a swap dropped: the live handoff tokens
// and job sessions whose runner the restored data does not hold.
type DroppedRunnerCredentials struct {
	// HandoffTokens is the count of the dropped handoff tokens.
	HandoffTokens int64
	// JobSessions is the count of the dropped job sessions.
	JobSessions int64
	// Runners is the ID of each runner with a dropped credential, sorted
	// and with no repetition.
	Runners []string
}

// NewDroppedRunnerCredentials returns the record of a swap that dropped
// handoffTokens handoff tokens and jobSessions job sessions, which name the
// runners runners. It sorts the runner IDs and removes repeats, so that the
// audit entry of the drop names each runner one time.
func NewDroppedRunnerCredentials(handoffTokens, jobSessions int64, runners []string) DroppedRunnerCredentials {
	out := DroppedRunnerCredentials{
		HandoffTokens: handoffTokens,
		JobSessions:   jobSessions,
		Runners:       slices.Clone(runners),
	}

	slices.Sort(out.Runners)
	out.Runners = slices.Compact(out.Runners)

	return out
}

// Empty reports whether the swap dropped no credential. An empty record
// writes no audit entry.
func (d DroppedRunnerCredentials) Empty() bool {
	return d.HandoffTokens == 0 && d.JobSessions == 0
}

// Entry returns the audit entry of the swap that dropped d, at the time at,
// in the scope @server. The server acts on its own here: the drop has no
// caller. The entry names the counts and the runner IDs, never a token
// value, a token hash or a session secret.
func (d DroppedRunnerCredentials) Entry(at time.Time) audit.Entry {
	return audit.Entry{
		At:            at.UTC(),
		Namespace:     audit.ServerScope,
		Subject:       history.ServerSubject,
		IDPSubject:    "",
		TokenID:       "",
		Action:        ActionDroppedRunnerCredentials,
		Object:        d.Object(),
		Version:       "",
		ValuePath:     "",
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
}

// Object returns the object text of the entry of d: the counts of the
// dropped credentials and the runner IDs. It names no token value, no token
// hash and no session secret.
func (d DroppedRunnerCredentials) Object() string {
	return "restore dropped " + countText(d.HandoffTokens, "handoff token") + " and " +
		countText(d.JobSessions, "job session") + ": the restored data does not hold " + runnerText(d.Runners)
}

// countText returns n with unit in the singular for 1 and in the plural for
// every other count.
func countText(n int64, unit string) string {
	if n == 1 {
		return "1 " + unit
	}

	return strconv.FormatInt(n, 10) + " " + unit + "s"
}

// runnerText names one runner, or each runner of several, sorted and with no
// repetition.
func runnerText(runners []string) string {
	if len(runners) == 1 {
		return "runner " + runners[0]
	}

	return "runners " + strings.Join(runners, ", ")
}
