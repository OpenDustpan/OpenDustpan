// Package declaration reads the file that declares a provider: its
// configuration, its engine, and each resource and data source with the HTTP
// requests or commands that implement them. One declaration builds one
// provider that works with OpenTofu and with the Pulumi CLI.
//
// The file is JSON. Its first key is "schema", the version of the format.
// Parse refuses a version that it does not know and every key that the
// version does not define, so a typing error fails the build and not the
// apply.
//
// A string in a request or a command can hold references in braces:
//
//	{name}               the attribute "name" of the resource or data source
//	{config.name}        the attribute "name" of the provider configuration
//	{file:config.name}   the path of a file of mode 0600 that holds the value
//	                     of the configuration attribute "name"
//
// A sensitive value never goes in a command argument, an environment
// variable, a URL path or a query: the process list, the logs of a proxy and
// the history of a shell can show those. It goes in a request body, on the
// standard input, through authentication, or as a file.
package declaration

// SchemaVersion is the version of the declaration format that this package
// reads.
const SchemaVersion = 1

// Document is one declaration file.
type Document struct {
	// Schema is the version of the format. It must be SchemaVersion.
	Schema int `json:"schema"`
	// Provider names the provider and declares its configuration.
	Provider Provider `json:"provider"`
	// HTTP selects the HTTP engine. Exactly one of HTTP and CLI is set.
	HTTP *HTTPEngine `json:"http,omitempty"`
	// CLI selects the CLI engine. Exactly one of HTTP and CLI is set.
	CLI *CLIEngine `json:"cli,omitempty"`
	// Resources are the resource types, in the order of the documentation.
	Resources []Resource `json:"resources,omitempty"`
	// DataSources are the data source types, in the order of the
	// documentation.
	DataSources []DataSource `json:"dataSources,omitempty"`
}

// Provider names the provider and declares its configuration.
type Provider struct {
	// Name is the provider type name: a lowercase letter, then lowercase
	// letters and digits, at most 32 characters.
	Name string `json:"name"`
	// Description is the text of the documentation.
	Description string `json:"description,omitempty"`
	// Config declares the attributes of the provider configuration. Each is
	// required or optional. A credential is a sensitive attribute here and
	// nowhere else.
	Config []Attribute `json:"config,omitempty"`
}

// Attribute declares one attribute.
type Attribute struct {
	// Name is snake_case.
	Name string `json:"name"`
	// Type is "string", "number", "bool", "list(T)" or "map(T)".
	Type string `json:"type"`
	// Mode is "required", "optional", "computed" or "optionalComputed".
	Mode string `json:"mode"`
	// Sensitive masks the value in plans and logs.
	Sensitive bool `json:"sensitive,omitempty"`
	// WriteOnly sends the value and never stores it in state.
	WriteOnly bool `json:"writeOnly,omitempty"`
	// Replace makes a change replace the object.
	Replace bool `json:"replace,omitempty"`
	// Description is the text of the documentation.
	Description string `json:"description,omitempty"`
}

// Resource declares one resource type.
type Resource struct {
	// Name is the type name without the provider prefix, in snake_case.
	Name string `json:"name"`
	// Attributes must include "id".
	Attributes []Attribute `json:"attributes"`
	// Create makes the object. Its reply or output gives the id.
	Create Operation `json:"create"`
	// Read gets the object by id. A not-found answer removes the object
	// from state, which shows as drift in the next plan.
	Read Operation `json:"read"`
	// Update changes the object in place. Without it, every attribute that
	// the user sets must force a replace.
	Update *Operation `json:"update,omitempty"`
	// Delete removes the object. A not-found answer counts as done.
	Delete Operation `json:"delete"`
}

// DataSource declares one data source type.
type DataSource struct {
	// Name is the type name without the provider prefix, in snake_case.
	Name string `json:"name"`
	// Attributes are the inputs (required or optional) and the results
	// (computed).
	Attributes []Attribute `json:"attributes"`
	// Read gets the results.
	Read Operation `json:"read"`
}

// Operation is one HTTP request or one command. The engine of the document
// decides which one it must be.
type Operation struct {
	// Request is set for the HTTP engine.
	Request *Request `json:"request,omitempty"`
	// Command is set for the CLI engine.
	Command *Command `json:"command,omitempty"`
}

// HTTPEngine is the configuration of the HTTP engine.
type HTTPEngine struct {
	// BaseURL is the URL that request paths are relative to. It can hold
	// references to the configuration, for example "{config.endpoint}". It
	// must be https, unless the user sets it to a loopback address.
	BaseURL string `json:"baseURL"`
	// Auth says how the engine authenticates each request.
	Auth Auth `json:"auth"`
	// Retry says when the engine sends a request again.
	Retry Retry `json:"retry,omitzero"`
	// Timeout is the limit for one attempt, as a Go duration. The default
	// is 30s.
	Timeout string `json:"timeout,omitempty"`
	// OpenAPI is the path of the OpenAPI document that the build reads to
	// fill attribute types and descriptions. The built provider does not
	// read it.
	OpenAPI string `json:"openAPI,omitempty"`
}

// The kinds of HTTP authentication.
const (
	AuthNone   = "none"
	AuthBearer = "bearer"
	AuthHeader = "header"
	AuthBasic  = "basic"
	AuthOAuth2 = "oauth2ClientCredentials"
)

// Auth says how the HTTP engine authenticates. Each credential field is a
// reference to a sensitive configuration attribute, for example
// "{config.token}". The engine adds the credential after it records the
// request for logs, so a log never holds it.
type Auth struct {
	// Kind is "none", "bearer", "header", "basic" or
	// "oauth2ClientCredentials".
	Kind string `json:"kind"`
	// Token is the bearer token, or the value of the header.
	Token string `json:"token,omitempty"`
	// Header is the name of the header for the kind "header".
	Header string `json:"header,omitempty"`
	// Username is the user for the kind "basic". It need not be sensitive.
	Username string `json:"username,omitempty"`
	// Password is the password for the kind "basic".
	Password string `json:"password,omitempty"`
	// TokenURL is the token endpoint for "oauth2ClientCredentials".
	TokenURL string `json:"tokenURL,omitempty"`
	// ClientID is the client for "oauth2ClientCredentials".
	ClientID string `json:"clientID,omitempty"`
	// ClientSecret is the secret for "oauth2ClientCredentials".
	ClientSecret string `json:"clientSecret,omitempty"`
	// Scopes are the scopes for "oauth2ClientCredentials".
	Scopes []string `json:"scopes,omitempty"`
}

// Retry says when the HTTP engine sends a request again. The engine retries
// a network failure and each status in Statuses. It retries a POST or a
// PATCH only when the request is idempotent.
type Retry struct {
	// MaxAttempts is the number of attempts, including the first. The
	// default is 4.
	MaxAttempts int `json:"maxAttempts,omitempty"`
	// Statuses are the HTTP statuses to retry. The default is 429, 502, 503
	// and 504.
	Statuses []int `json:"statuses,omitempty"`
	// MaxWait is the longest wait between two attempts, as a Go duration.
	// The engine follows a Retry-After header up to this limit. The default
	// is 30s.
	MaxWait string `json:"maxWait,omitempty"`
}

// Request is one HTTP request of the HTTP engine.
type Request struct {
	// Method is GET, POST, PUT, PATCH or DELETE.
	Method string `json:"method"`
	// Path starts with "/". A reference fills a whole path segment, and the
	// engine escapes it.
	Path string `json:"path"`
	// Query maps each query parameter to a literal or a reference.
	Query map[string]string `json:"query,omitempty"`
	// Body maps a JSON pointer in the request body to a literal or a
	// reference. An empty map sends no body.
	Body map[string]string `json:"body,omitempty"`
	// Response maps an attribute to a JSON pointer in the reply body, or in
	// the selected item for a list.
	Response map[string]string `json:"response,omitempty"`
	// NotFound are the statuses that mean "gone". The default is 404.
	NotFound []int `json:"notFound,omitempty"`
	// Idempotent allows a retry of a POST or a PATCH, for an API that
	// accepts an idempotency key.
	Idempotent bool `json:"idempotent,omitempty"`
	// List reads a list of items, page by page, and selects one item. A
	// data source over a list endpoint uses it.
	List *List `json:"list,omitempty"`
}

// The kinds of pagination.
const (
	PageNone   = "none"
	PageLink   = "linkHeader"
	PageCursor = "cursor"
	PageOffset = "offset"
)

// List reads a list of items and selects the one item that matches.
type List struct {
	// Items is the JSON pointer of the array of items in each page.
	Items string `json:"items"`
	// Match maps a JSON pointer in an item to a literal or a reference. The
	// item must match all entries. No match is "not found". More than one
	// match is an error.
	Match map[string]string `json:"match"`
	// Pages is "none", "linkHeader", "cursor" or "offset".
	Pages string `json:"pages"`
	// CursorParam is the query parameter that takes the cursor.
	CursorParam string `json:"cursorParam,omitempty"`
	// Cursor is the JSON pointer of the next cursor in the reply. An empty or
	// missing cursor ends the list.
	Cursor string `json:"cursor,omitempty"`
	// OffsetParam is the query parameter that takes the offset.
	OffsetParam string `json:"offsetParam,omitempty"`
	// LimitParam is the query parameter that takes the page size.
	LimitParam string `json:"limitParam,omitempty"`
	// PageSize is the value of LimitParam. The default is 100.
	PageSize int `json:"pageSize,omitempty"`
	// MaxPages stops a list that does not end. The default is 100.
	MaxPages int `json:"maxPages,omitempty"`
}

// CLIEngine is the configuration of the CLI engine.
type CLIEngine struct {
	// Program is a bare name that the engine finds on PATH, or a clean
	// absolute path. The engine starts it with no shell.
	Program string `json:"program"`
	// Env sets environment variables of the program. A value is a literal
	// or a reference, never a sensitive one: give a credential as a file
	// with "{file:config.name}". The engine passes no other variable except
	// PATH, HOME and TMPDIR.
	Env map[string]string `json:"env,omitempty"`
	// Timeout is the limit for one command, as a Go duration. The default
	// is 5m.
	Timeout string `json:"timeout,omitempty"`
}

// Command is one run of the program of the CLI engine.
type Command struct {
	// Args are the arguments after the program, one element each. A
	// reference can fill a whole element or a part of it. A value that
	// fills the start of an element and starts with "-" fails the command,
	// so a value cannot become an option.
	Args []string `json:"args"`
	// Stdin maps a JSON pointer in a JSON object on the standard input to a
	// literal or a reference. An empty map gives an empty standard input.
	Stdin map[string]string `json:"stdin,omitempty"`
	// Output maps an attribute to a JSON pointer in the JSON on the
	// standard output.
	Output map[string]string `json:"output,omitempty"`
	// NotFound are the exit codes that mean "gone".
	NotFound []int `json:"notFound,omitempty"`
}
