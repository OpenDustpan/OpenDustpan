// Package jobproxy holds the plain-data core of the job proxy: the proxy
// rule set, its parser, the render of its templates when a job starts and
// the match of one request against the rendered proxy rules.
//
// A proxy rule only allows. A request that no proxy rule allows is denied
// with a fixed reason. The package never normalizes a path: a path that is
// not canonical is refused, so each allowed path has one spelling and the
// job proxy sends the path that it checked with no change.
//
// The launcher links this package, so it links no internal package, no SQL
// driver, no forge client and no other package of the server. The store port
// is in package rulestore.
package jobproxy

import (
	"errors"
	"regexp"
)

// Errors of the package.
var (
	// ErrInvalid is the error for a proxy rule set that Parse refuses. The
	// wrapped text names the proxy rule and the field, never a value of a
	// variable.
	ErrInvalid = errors.New("the proxy rule set is not valid")
	// ErrRender is the error for a proxy rule that Render cannot render. The
	// wrapped text names the proxy rule and the variable, never the value.
	ErrRender = errors.New("the proxy rule cannot be rendered")
	// ErrCredential is the error for a proxy rule that names a credential, a
	// client certificate or a CA that no profile of the unit gives. The start
	// of the job fails with it.
	ErrCredential = errors.New("the proxy rule names an export that no profile of the unit gives")
)

// Limits of a proxy rule set.
const (
	// MaxDocumentBytes is the largest proxy rule set document.
	MaxDocumentBytes = 64 << 10
	// MaxRules is the largest number of proxy rules in one proxy rule set.
	MaxRules = 256
	// MaxIDBytes is the longest ID of a proxy rule.
	MaxIDBytes = 64
	// MaxNameBytes is the longest name of an export or a variable part.
	MaxNameBytes = 64
	// MaxPaths is the largest number of path patterns in one proxy rule.
	MaxPaths = 64
	// MaxPathBytes is the longest path pattern and the longest path that
	// CanonicalPath accepts.
	MaxPathBytes = 2048
	// MaxHostBytes is the longest host name.
	MaxHostBytes = 253
	// DefaultPort is the port of a proxy rule with no port.
	DefaultPort = 443
	// maxPort is the highest TCP port.
	maxPort = 65535
	// maxLabelBytes is the longest label of a DNS name.
	maxLabelBytes = 63
)

// ServerTrustSystem is the server trust of a proxy rule that names no CA
// export: the job proxy checks the server with the roots of the system.
const ServerTrustSystem = "system"

// Operation is the operation of a job that a proxy rule allows.
type Operation string

// The operations.
const (
	OperationPlan    Operation = "plan"
	OperationApply   Operation = "apply"
	OperationDestroy Operation = "destroy"
	OperationRefresh Operation = "refresh"
)

// Valid reports whether o is a known operation.
func (o Operation) Valid() bool {
	switch o {
	case OperationPlan, OperationApply, OperationDestroy, OperationRefresh:
		return true
	default:
		return false
	}
}

// Mode is the way the job proxy handles the connections of a proxy rule.
type Mode string

// The modes.
const (
	// ModeInspect ends the TLS of the client, checks the method and the path
	// of each request, and can add a credential.
	ModeInspect Mode = "inspect"
	// ModeTunnel checks the host only and passes the bytes of the connection.
	ModeTunnel Mode = "tunnel"
	// ModeEndpoint serves the host on a loopback port of the job and checks
	// the method and the path of each request.
	ModeEndpoint Mode = "endpoint"
)

// Valid reports whether m is a known mode.
func (m Mode) Valid() bool {
	switch m {
	case ModeInspect, ModeTunnel, ModeEndpoint:
		return true
	default:
		return false
	}
}

// checksRequests reports whether the job proxy sees the method and the path
// of each request of the mode.
func (m Mode) checksRequests() bool {
	return m == ModeInspect || m == ModeEndpoint
}

// Rule is one proxy rule as Parse returns it. Port and ServerTrust hold
// their effective values: Parse fills DefaultPort and ServerTrustSystem when
// the document leaves them out.
type Rule struct {
	ID                string      `json:"id"`
	Operations        []Operation `json:"operations"`
	Host              string      `json:"host"`
	Port              int         `json:"port"`
	Mode              Mode        `json:"mode"`
	Methods           []string    `json:"methods,omitempty"`
	Paths             []string    `json:"paths,omitempty"`
	Credential        string      `json:"credential,omitempty"`
	ClientCertificate string      `json:"clientCertificate,omitempty"`
	ServerTrust       string      `json:"serverTrust"`
}

// RuleSet is a proxy rule set: the proxy rules of one scope, or the joined
// proxy rules of the scopes of a unit.
type RuleSet struct {
	Rules []Rule `json:"rules"`
}

// methods is the allowlist of request methods that a proxy rule can name.
// CONNECT and TRACE are not in it.
func methods() []string {
	return []string{"GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"}
}

// idPattern is the shape of the ID of a proxy rule.
var idPattern = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)

// namePattern is the shape of the name of an export, a variable part and a
// profile.
var namePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
