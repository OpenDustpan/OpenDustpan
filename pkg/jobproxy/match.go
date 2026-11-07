package jobproxy

import (
	"slices"
	"strings"
)

// Reason is the fixed reason of a denied request. A record and the job log
// hold it, never the text of a library error.
type Reason string

// The fixed reasons of Match.
const (
	// ReasonNone is the reason of an allowed request.
	ReasonNone Reason = ""
	// ReasonNoRuleHost: no proxy rule names the host and the port for the
	// operation of the job.
	ReasonNoRuleHost Reason = "no-rule-host"
	// ReasonNoRuleMatched: a proxy rule names the host, but none allows the
	// method and the path.
	ReasonNoRuleMatched Reason = "no-rule-matched"
	// ReasonPathNotCanonical: the path is not canonical (see CanonicalPath).
	ReasonPathNotCanonical Reason = "path-not-canonical"
	// ReasonPlainHTTPInjection: the proxy rule that allows the request adds
	// a credential, and the request goes to the host with plain HTTP.
	ReasonPlainHTTPInjection Reason = "credential-over-http"
)

// Request is one request, or one tunnel, that the job proxy checks.
type Request struct {
	Operation Operation
	Host      string
	Port      int
	// Method and Path are empty for a tunnel: the job proxy sees no request
	// in it. Path is the path of the request target without the query: the
	// caller removes "?" and the query before Match. No proxy rule checks the
	// query. A Path that holds "?" is not canonical, so Match denies it.
	Method string
	Path   string
	// PlainHTTP is true when the job proxy sends the request to the host
	// with no TLS.
	PlainHTTP bool
}

// Decision is the answer of Match. RuleID names the proxy rule that allows
// the request. Reason is ReasonNone when Allowed is true, and a fixed reason
// when it is false.
type Decision struct {
	Allowed bool
	RuleID  string
	Reason  Reason
}

// Match checks req against the rendered proxy rules. It compares the host,
// the method and each path segment byte for byte: it never changes the case
// of a host or a path, and never removes a dot at the end of a host. The
// first proxy rule, in order, that allows the request decides.
//
// A tunnel proxy rule allows each connection to its host and port. An
// inspect or endpoint proxy rule allows a request whose method it lists and whose canonical
// path one of its path patterns matches. "*" matches one segment, "**"
// matches one or more segments.
func Match(r Rendered, req Request) Decision {
	hostRules := r.forHost(req)

	if len(hostRules) == 0 {
		return deny(ReasonNoRuleHost)
	}

	// One host and port have one mode (Validate), so the first proxy rule tells
	// the mode of each.
	if hostRules[0].Mode == ModeTunnel {
		return Decision{Allowed: true, RuleID: hostRules[0].ID, Reason: ReasonNone}
	}

	if _, ok := CanonicalPath(req.Path); !ok {
		return deny(ReasonPathNotCanonical)
	}

	segs := pathSegments(req.Path)

	for _, rr := range hostRules {
		if rr.Mode == ModeTunnel || !slices.Contains(rr.Methods, req.Method) || !rr.matchesPath(segs) {
			continue
		}

		if rr.Credential != "" && req.PlainHTTP {
			return Decision{Allowed: false, RuleID: rr.ID, Reason: ReasonPlainHTTPInjection}
		}

		return Decision{Allowed: true, RuleID: rr.ID, Reason: ReasonNone}
	}

	return deny(ReasonNoRuleMatched)
}

// forHost returns the rendered proxy rules that name the host and the port
// of req for its operation, in order.
func (r Rendered) forHost(req Request) []*renderedRule {
	out := make([]*renderedRule, 0, len(r.rules))

	for i := range r.rules {
		rr := &r.rules[i]
		if rr.Host == req.Host && rr.Port == req.Port && slices.Contains(rr.Operations, req.Operation) {
			out = append(out, rr)
		}
	}

	return out
}

// deny returns a denial with reason.
func deny(reason Reason) Decision {
	return Decision{Allowed: false, RuleID: "", Reason: reason}
}

// pathSegments splits a canonical path into its segments. "/" has none.
func pathSegments(p string) []string {
	if p == "/" {
		return nil
	}

	return strings.Split(p[1:], "/")
}

// matchesPath reports whether a path pattern of rr matches segs.
func (rr *renderedRule) matchesPath(segs []string) bool {
	return slices.ContainsFunc(rr.patterns, func(pattern []segment) bool {
		return matchSegments(pattern, segs)
	})
}

// matchSegments reports whether pattern matches segs.
func matchSegments(pattern []segment, segs []string) bool {
	for i, s := range pattern {
		switch s.kind {
		case segmentRest:
			// "**" is last, and matches one or more segments.
			return len(segs) > i
		case segmentOne:
			if i >= len(segs) {
				return false
			}
		case segmentLiteral, segmentVariable:
			// A rendered pattern holds no variable, so a variable segment
			// matches no segment (fail closed).
			if s.kind != segmentLiteral || i >= len(segs) || segs[i] != s.text {
				return false
			}
		default:
			// An unknown kind matches no segment (fail closed).
			return false
		}
	}

	return len(pattern) == len(segs)
}
