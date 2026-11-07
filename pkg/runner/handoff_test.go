package runner_test

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/runner"
)

const hostileSecret = runner.HandoffTokenPrefix + "hostile-secret-value"

var errDriver = errors.New("driver text")

// none is the refusal that a constructor returns for an error that is no
// refusal.
func none() runner.HandoffRefusal {
	return runner.HandoffRefusal{Reason: "", Runner: "", Verified: false}
}

func refusalOf(reason runner.HandoffReason, id string, verified bool) runner.HandoffRefusal {
	return runner.HandoffRefusal{Reason: reason, Runner: id, Verified: verified}
}

func TestNewHandoffRefusal(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		err     error
		claimed string
		want    runner.HandoffRefusal
		ok      bool
	}{
		{
			"unknown",
			fmt.Errorf("select: %w", runner.ErrNotFound),
			"",
			refusalOf(runner.HandoffUnknown, "", false),
			true,
		},
		{
			"expired",
			fmt.Errorf("check: %w", runner.ErrTokenExpired),
			"",
			refusalOf(runner.HandoffExpired, "", false),
			true,
		},
		{"used", fmt.Errorf("check: %w", runner.ErrTokenUsed), "", refusalOf(runner.HandoffUsed, "", false), true},
		{"claim lost", runner.ErrClaimLost, "", refusalOf(runner.HandoffClaimLost, "", false), true},
		{"wrong runner", runner.ErrWrongRunner, "", refusalOf(runner.HandoffWrongRunner, "", false), true},
		{
			"claim of the form of an ID",
			runner.ErrNotFound,
			"rn-abc123",
			refusalOf(runner.HandoffUnknown, "rn-abc123", false),
			true,
		},
		{"token as claim", runner.ErrTokenUsed, hostileSecret, refusalOf(runner.HandoffUsed, "", false), true},
		{"line break in claim", runner.ErrTokenUsed, "rn-abc\nforged", refusalOf(runner.HandoffUsed, "", false), true},
		{
			"claim too long",
			runner.ErrTokenUsed,
			"rn-" + strings.Repeat("a", 100),
			refusalOf(runner.HandoffUsed, "", false),
			true,
		},
		{"prefix only", runner.ErrTokenUsed, runner.RunnerIDPrefix, refusalOf(runner.HandoffUsed, "", false), true},
		{"database error", errDriver, "rn-abc123", none(), false},
		{"bad proof", runner.ErrBadProof, "", none(), false},
		{"no error", nil, "", none(), false},
	}

	for _, tc := range cases {
		got, ok := runner.NewHandoffRefusal(tc.err, tc.claimed)
		if ok != tc.ok || got != tc.want {
			t.Errorf("%s: NewHandoffRefusal = %+v, %v, want %+v, %v", tc.name, got, ok, tc.want, tc.ok)
		}
	}
}

func TestNewVerifiedHandoffRefusal(t *testing.T) {
	t.Parallel()

	got, ok := runner.NewVerifiedHandoffRefusal(runner.ErrWrongRunner, "rn-abc123")
	if !ok || got != refusalOf(runner.HandoffWrongRunner, "rn-abc123", true) {
		t.Errorf("NewVerifiedHandoffRefusal = %+v, %v, want wrong-runner of rn-abc123, verified", got, ok)
	}

	if got, ok = runner.NewVerifiedHandoffRefusal(errDriver, "rn-abc123"); ok || got != none() {
		t.Errorf("NewVerifiedHandoffRefusal of a database error = %+v, %v, want none", got, ok)
	}
}

// TestWrongRunnerIsAClaimLoss proves that ErrWrongRunner keeps every check for
// ErrClaimLost working, so the answer to the runner does not change.
func TestWrongRunnerIsAClaimLoss(t *testing.T) {
	t.Parallel()

	if !errors.Is(runner.ErrWrongRunner, runner.ErrClaimLost) {
		t.Error("ErrWrongRunner does not wrap ErrClaimLost")
	}
}

// producer is one case of the table of producers: the event, and either the
// code of this package that makes it, or the adapter file that must name it.
type producer struct {
	event  runner.Event
	made   func() runner.Event
	file   string
	symbol string
}

func producers() []producer {
	refusal, _ := runner.NewHandoffRefusal(runner.ErrNotFound, "")
	rejoined := new(runner.Joined)
	rejoined.Rejoined = true

	return []producer{
		{runner.EventPoolCreated, nil, "admin.go", "runner.EventPoolCreated"},
		{runner.EventPoolChanged, nil, "admin.go", "runner.EventPoolChanged"},
		{runner.EventPoolDeleted, nil, "admin.go", "runner.EventPoolDeleted"},
		{runner.EventJoinTokenCreated, nil, "admin.go", "runner.EventJoinTokenCreated"},
		{runner.EventJoinTokenRevoked, nil, "admin.go", "runner.EventJoinTokenRevoked"},
		{runner.EventRunnerDrained, nil, "admin.go", "runner.EventRunnerDrained"},
		{runner.EventRunnerRetired, nil, "admin.go", "runner.EventRunnerRetired"},
		{runner.EventKeyRotated, nil, "protocol.go", "runner.EventKeyRotated"},
		{runner.EventRunnerJoined, func() runner.Event { return runner.JoinEvent(new(runner.Joined)) }, "", ""},
		{runner.EventRunnerRejoined, func() runner.Event { return runner.JoinEvent(rejoined) }, "", ""},
		{runner.EventHandoffRefused, refusal.Event, "handoffaudit.go", "ref.Event()"},
	}
}

// check reports the problems of one case: the producer made another event, the
// event has no rule, or the adapter file does not name it.
func (p *producer) check(adapter *os.Root) []string {
	var problems []string

	if _, ok := runner.AuditRules()[p.event]; !ok {
		problems = append(problems, "case for event "+string(p.event)+", which has no audit rule")
	}

	if p.made != nil && p.made() != p.event {
		problems = append(problems, fmt.Sprintf("event %s: the producer made %s", p.event, p.made()))
	}

	if p.file == "" {
		return problems
	}

	src, err := fs.ReadFile(adapter.FS(), p.file)
	if err != nil || !strings.Contains(string(src), p.symbol) {
		problems = append(problems, fmt.Sprintf("event %s: %s does not name %s (%v)", p.event, p.file, p.symbol, err))
	}

	return problems
}

// TestEveryAuditEventHasAProducer fails when an event of AuditRules is not
// produced by any case of the table of producers, and when a case names an
// event that has no rule. A case either calls the code of this package that
// makes the event, or names the adapter file that must name the event.
func TestEveryAuditEventHasAProducer(t *testing.T) {
	t.Parallel()

	adapter, err := os.OpenRoot(filepath.Join("..", "..", "internal", "api", "runnerapi"))
	if err != nil {
		t.Fatalf("open the adapter directory: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := adapter.Close(); closeErr != nil {
			t.Errorf("close the adapter directory: %v", closeErr)
		}
	})

	// Events with a rule and no producer yet. A case for one of them fails the
	// test, so this list shrinks when its producer lands.
	unproduced := map[runner.Event]bool{runner.EventCredentialsIssued: true}
	claimed := map[runner.Event]bool{}

	for _, p := range producers() {
		if claimed[p.event] || unproduced[p.event] {
			t.Errorf("event %s has two cases, or has a case and is listed as unproduced", p.event)
		}

		claimed[p.event] = true

		for _, problem := range p.check(adapter) {
			t.Error(problem)
		}
	}

	for event := range runner.AuditRules() {
		if !claimed[event] && !unproduced[event] {
			t.Errorf("event %s has an audit rule and no producer in the table", event)
		}
	}
}
