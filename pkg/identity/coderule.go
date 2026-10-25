package identity

// CodeRuleID names one rule of the code-rule catalog. It is a stable,
// lowercase identifier with words joined by '-', for example
// "job-token-unit-limit". An ID never changes its meaning.
type CodeRuleID string

// CodeRuleEffect is what a rule of the code-rule catalog does to a decision.
type CodeRuleEffect string

// The effects of a code rule.
const (
	// CodeRuleAllows allows an action with no stored grant, for a credential
	// that authenticates no principal: for example, a runner key that claims
	// a job of its pool. Such a rule names at least one action and at least
	// one credential. The one rule that allows an action to a credential of
	// a principal is the revoke of the tokens and the sessions of the
	// caller, and it names its action and its credentials exactly. An
	// "explain why" answer names the rule for each action that it allows.
	CodeRuleAllows CodeRuleEffect = "allows"
	// CodeRuleLimits takes away an action that a grant gives: for example,
	// a job token acts on the unit of its job only. A limit never allows an
	// action.
	CodeRuleLimits CodeRuleEffect = "limits"
	// CodeRuleExempts keeps one check away from a principal or a credential:
	// for example, the access limits do not apply to a session of a
	// break-glass account. An exemption never allows an action that no grant
	// gives.
	CodeRuleExempts CodeRuleEffect = "exempts"
)

// CodeRule is one rule of the code-rule catalog: a rule in code that takes
// part in a decision of access with no stored grant. Code may define roles,
// actions and these rules. It never defines the holder of a grant.
//
// The catalog is the one list of every such rule. The "explain why" answer
// shows the rules that applied to a decision, and a test fails when a new
// rule in code has no entry in the catalog. The change that fills the
// catalog adds the function that returns it, with its guard test.
//
// Summary is one sentence for people to read. Actions holds the actions
// that the rule decides. Credentials and Kinds name the requests that the
// rule applies to: a request matches when its credential is in Credentials
// and its kind is in Kinds. For a rule with the effect limits or exempts,
// an empty list matches every value. A rule with the effect allows never
// has an empty Actions or Credentials list: it would allow every action or
// every principal. Where names the package and the
// symbol that applies the rule, so that a reviewer finds it.
type CodeRule struct {
	ID          CodeRuleID
	Effect      CodeRuleEffect
	Summary     string
	Where       string
	Actions     []Action
	Credentials []Credential
	Kinds       []Kind
}
