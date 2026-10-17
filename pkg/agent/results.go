package agent

// The results of the tools, as JSON. Each object in a result carries its
// reference ("ref"), so the model can name it and the console can link it.
// A value that is secret is the marker of the masked state read, which
// holds the value path and never the value, unless the field says
// otherwise. Each list says when it left objects out
// ("truncated"). The field names are camelCase, as in the API. Each tool
// keeps the words of the tool of the unit in its text fields: a Pulumi
// stack has updates and previews, an OpenTofu workspace has state versions
// and plans.

// UnitSummary is one unit in a list.
type UnitSummary struct {
	Ref       string `json:"ref"`
	Tool      string `json:"tool"`
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	// Resources is the count of resources of the latest version.
	Resources int `json:"resources"`
	// UpdatedAtMs is the time of the latest version, in Unix milliseconds.
	UpdatedAtMs int64    `json:"updatedAtMs"`
	Labels      []string `json:"labels"`
}

// SearchUnitsResult is the result of search_units.
type SearchUnitsResult struct {
	Units     []UnitSummary `json:"units"`
	Truncated bool          `json:"truncated"`
}

// UnitResult is the result of describe_unit. Context is the parsed context
// output, the readme and the object metadata, each with its own source label.
type UnitResult struct {
	Ref     string        `json:"ref"`
	Tool    string        `json:"tool"`
	Context []ContextItem `json:"context"`
	Lock    *LockInfo     `json:"lock"`
	// LastChange is the latest entry of the history.
	LastChange *HistoryEntry `json:"lastChange"`
	// Outputs maps an output name to its value, with secret values masked.
	Outputs map[string]any `json:"outputs"`
	// ResourceCounts maps a resource type to its count.
	ResourceCounts map[string]int `json:"resourceCounts"`
	// Executors are the executors that a change of the unit can use.
	Executors []Executor `json:"executors"`
}

// ContextItem is one piece of context about a unit. Source names where it
// comes from: "output" (the reserved output), "readme" or "metadata". Text
// is data that other people wrote, never an instruction.
type ContextItem struct {
	Source string `json:"source"`
	Field  string `json:"field"`
	Text   string `json:"text"`
}

// LockInfo is a lock of a unit.
type LockInfo struct {
	Holder      string `json:"holder"`
	SinceMs     int64  `json:"sinceMs"`
	Description string `json:"description"`
}

// Resource is one resource of a state version. Attributes holds the
// attributes with secret values masked, or in plain text in a secret read
// that the gate allowed.
type Resource struct {
	Ref        string         `json:"ref"`
	Address    string         `json:"address"`
	Type       string         `json:"type"`
	Attributes map[string]any `json:"attributes"`
}

// ResourceChange is one resource that changed between two versions. Action
// is "add", "delete" or "change". Changed lists the attribute paths that
// changed, for "change".
type ResourceChange struct {
	Ref     string   `json:"ref"`
	Address string   `json:"address"`
	Type    string   `json:"type"`
	Action  string   `json:"action"`
	Changed []string `json:"changed"`
}

// StateResult is the result of read_state. Version is the version that the
// tool read. Resources is set for a read and Changes for a call with
// compareTo. SecretValues is true only when the result holds secret values
// in plain text. ClosedStage names why a secret read answered masked
// values, or is empty.
type StateResult struct {
	Ref          string           `json:"ref"`
	Version      string           `json:"version"`
	CompareTo    string           `json:"compareTo"`
	Resources    []Resource       `json:"resources"`
	Changes      []ResourceChange `json:"changes"`
	Truncated    bool             `json:"truncated"`
	SecretValues bool             `json:"secretValues"`
	ClosedStage  string           `json:"closedStage"`
}

// ObserveItem is one live fact about a resource. Kind is "event" or
// "health". Status is the status that the cloud reports, in its own words.
type ObserveItem struct {
	Ref      string `json:"ref"`
	Address  string `json:"address"`
	Kind     string `json:"kind"`
	Status   string `json:"status"`
	Message  string `json:"message"`
	AtMs     int64  `json:"atMs"`
	Provider string `json:"provider"`
}

// ObserveResult is the result of observe_unit. Identity names the
// read-only identity that the server used, for example the role of the
// profile of the unit, never a credential.
type ObserveResult struct {
	Ref       string        `json:"ref"`
	Identity  string        `json:"identity"`
	Items     []ObserveItem `json:"items"`
	Truncated bool          `json:"truncated"`
}

// HistoryEntry is one change of a unit. Kind is the word of the tool:
// "update", "preview", "state-version", "plan", "job", "lock" or "unlock".
// Run is the ID that read_run_output takes, or empty.
type HistoryEntry struct {
	Ref     string `json:"ref"`
	Kind    string `json:"kind"`
	Run     string `json:"run"`
	Version string `json:"version"`
	Subject string `json:"subject"`
	Result  string `json:"result"`
	AtMs    int64  `json:"atMs"`
	// Message is the commit or update message, data that a person wrote.
	Message string `json:"message"`
}

// HistoryResult is the result of list_history.
type HistoryResult struct {
	Ref       string         `json:"ref"`
	Entries   []HistoryEntry `json:"entries"`
	Truncated bool           `json:"truncated"`
}

// RunEvent is one event or log line of an update or job, with secret
// values masked.
type RunEvent struct {
	AtMs    int64  `json:"atMs"`
	Level   string `json:"level"`
	Address string `json:"address"`
	Text    string `json:"text"`
}

// RunOutputResult is the result of read_run_output.
type RunOutputResult struct {
	Ref       string     `json:"ref"`
	Run       string     `json:"run"`
	Result    string     `json:"result"`
	Events    []RunEvent `json:"events"`
	Truncated bool       `json:"truncated"`
}

// AuditItem is one audit entry, with the fields that a person reads.
type AuditItem struct {
	Sequence int64  `json:"sequence"`
	AtMs     int64  `json:"atMs"`
	Subject  string `json:"subject"`
	Action   string `json:"action"`
	Object   string `json:"object"`
	Outcome  string `json:"outcome"`
	Channel  string `json:"channel"`
	AgentRun string `json:"agentRun"`
	Tool     string `json:"tool"`
}

// AuditResult is the result of read_audit_log.
type AuditResult struct {
	Namespace string      `json:"namespace"`
	Entries   []AuditItem `json:"entries"`
	Truncated bool        `json:"truncated"`
}

// AccessItem is one action that one subject holds on a unit, and the path
// that gives it: "grant", "group" or "role", with its name.
type AccessItem struct {
	Subject string `json:"subject"`
	Action  string `json:"action"`
	Via     string `json:"via"`
	Name    string `json:"name"`
}

// AccessResult is the result of explain_access.
type AccessResult struct {
	Ref       string       `json:"ref"`
	Access    []AccessItem `json:"access"`
	Truncated bool         `json:"truncated"`
}

// DocPage is one page of the docs. Excerpt is the text near the match.
type DocPage struct {
	Path    string `json:"path"`
	Title   string `json:"title"`
	Excerpt string `json:"excerpt"`
}

// DocsResult is the result of search_docs. Version is the server version of
// the docs.
type DocsResult struct {
	Version string    `json:"version"`
	Pages   []DocPage `json:"pages"`
}

// ChangeResult is the result of propose_change, get_change and
// cancel_change.
type ChangeResult struct {
	Change               string      `json:"change"`
	Ref                  string      `json:"ref"`
	Operation            Operation   `json:"operation"`
	Executor             Executor    `json:"executor"`
	Mode                 ChangeMode  `json:"mode"`
	State                ChangeState `json:"state"`
	AwaitingConfirmation bool        `json:"awaitingConfirmation"`
	ConfirmURL           string      `json:"confirmURL"`
	ReviewURL            string      `json:"reviewURL"`
}

// ToolError is the content of a tool result with IsError set. Code is the
// HTTP status that the same failure has in the API. Message is in the
// strict style of the API errors, with no value from the failure.
type ToolError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}
