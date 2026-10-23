package apidesctest

import (
	"flag"
	"fmt"
	"os"
	"slices"
	"strings"
)

// TestingM is the part of testing.M that RequireObserved wraps. It is the
// same interface that goleak.VerifyTestMain takes.
type TestingM interface {
	Run() int
}

// RequireObserved returns the TestingM for TestMain. After the tests it fails the
// binary when:
//   - a described operation has no 2xx answer that a wrapper saw, and the
//     allowlist does not name it: a new operation needs a test of its
//     success path;
//   - the allowlist names an operation that a test observes, or that no
//     wrapper knows: the entry must go, so the list only gets shorter.
//
// It checks nothing when the tests fail, when the run is partial (the flag
// -run, or -short), or when a late wrong answer is the only problem to
// report; a late wrong answer always fails the binary.
func RequireObserved(m TestingM, allowlist []string) ObservedMain {
	return ObservedMain{m: m, allowlist: allowlist}
}

// ObservedMain is what RequireObserved returns. Its Run runs the tests and then
// the check of the observed operations.
type ObservedMain struct {
	m         TestingM
	allowlist []string
}

// Run implements TestingM.
func (o ObservedMain) Run() int {
	code := o.m.Run()
	if code != 0 {
		return code
	}

	if n := seen.late.Load(); n > 0 {
		fmt.Fprintf(
			os.Stderr,
			"apidesctest: %d answers had a status that the description does not give, after the end of their test\n",
			n,
		)

		return 1
	}

	if partialRun() {
		return 0
	}

	seen.mu.Lock()
	problems := observationProblems(keys(seen.known), keys(seen.observed), o.allowlist)
	seen.mu.Unlock()

	if len(problems) == 0 {
		return 0
	}

	fmt.Fprintln(os.Stderr, "apidesctest: "+strings.Join(problems, "\napidesctest: "))

	return 1
}

// partialRun reports whether the flags select part of the tests.
func partialRun() bool {
	if run := flag.Lookup("test.run"); run != nil && run.Value.String() != "" {
		return true
	}

	short := flag.Lookup("test.short")

	return short != nil && short.Value.String() == "true"
}

func keys(set map[string]struct{}) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}

	slices.Sort(out)

	return out
}

// observationProblems compares the operations that the wrappers know with the
// ones that a test observed and with the allowlist. It returns one line for
// each difference, in order.
func observationProblems(known, observed, allowlist []string) []string {
	var problems []string

	for _, id := range known {
		if !slices.Contains(observed, id) && !slices.Contains(allowlist, id) {
			problems = append(problems, "no test observes a success answer of "+id+
				": add a test, or list it in the allowlist of unobserved operations")
		}
	}

	for _, id := range allowlist {
		switch {
		case slices.Contains(observed, id):
			problems = append(problems, id+" is in the allowlist of unobserved operations, and a test observes it: "+
				"remove it from the list")
		case !slices.Contains(known, id):
			problems = append(
				problems,
				id+" is in the allowlist of unobserved operations, and no handler of the tests "+
					"describes it: remove it from the list",
			)
		default:
			// The operation is known and no test observes it, as the list says.
		}
	}

	return problems
}
