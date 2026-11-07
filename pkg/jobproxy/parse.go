package jobproxy

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"slices"
	"strconv"
	"strings"

	"opendustpan.org/opendustpan/pkg/egress"
)

// Fixed reasons of a proxy rule that Parse refuses. A reason never holds a
// value of the document.
const (
	reasonID              = "the id must have 1 to 64 letters, digits or -"
	reasonIDTwice         = "the id is used by an earlier proxy rule"
	reasonOperations      = "the operations must list plan, apply, destroy or refresh, each once"
	reasonHost            = "the host must be an exact DNS name in lowercase, with no dot at the end"
	reasonHostIP          = "the host is an IP address that no allowed range holds"
	reasonPort            = "the port must be from 1 to 65535"
	reasonMode            = "the mode must be inspect, tunnel or endpoint"
	reasonModeTwice       = "an earlier proxy rule gives the same host and port another mode"
	reasonMethods         = "the methods must list GET, HEAD, POST, PUT, PATCH, DELETE or OPTIONS, each once"
	reasonPaths           = "the paths must list from 1 to 64 path patterns, each once"
	reasonTunnelRequest   = "a tunnel proxy rule cannot give methods, paths, a credential, a client certificate or a serverTrust other than system"
	reasonExportName      = "an export name must have 1 to 64 letters, digits, _ or -"
	reasonServerTrust     = "the serverTrust must be system or the name of a CA export"
	reasonTooManyRules    = "the proxy rule set has more than 256 proxy rules"
	reasonDocumentSize    = "the document is larger than 64 KiB"
	reasonDocumentShape   = "the document is not one JSON object with the field rules and known fields only"
	reasonDuplicateKey    = "the document holds a key twice in one object"
	reasonAllowedRange    = "an allowed range is too wide or is not a private range"
	reasonDocumentTooDeep = "the document is nested too deep"
)

// minLabels is the fewest labels of a host: a name with no dot is not a
// DNS name of the internet.
const minLabels = 2

// maxDepth is the deepest nesting of a proxy rule set document: the
// document, the list of proxy rules, one proxy rule and one list of it.
const maxDepth = 8

// document is the wire shape of a proxy rule set.
type document struct {
	Rules *[]documentRule `json:"rules"`
}

// documentRule is the wire shape of one proxy rule. Port is a pointer, so
// a missing port is told apart from a port of 0.
type documentRule struct {
	ID                string      `json:"id"`
	Operations        []Operation `json:"operations"`
	Host              string      `json:"host"`
	Port              *int        `json:"port"`
	Mode              Mode        `json:"mode"`
	Methods           []string    `json:"methods"`
	Paths             []string    `json:"paths"`
	Credential        string      `json:"credential"`
	ClientCertificate string      `json:"clientCertificate"`
	ServerTrust       string      `json:"serverTrust"`
}

// Parse parses a proxy rule set document and returns the proxy rule set with the
// effective port and server trust of each proxy rule. It returns ErrInvalid
// for a document over MaxDocumentBytes, a document that is not one JSON
// object, an unknown field, a key that an object holds twice, a set over
// MaxRules proxy rules, and a proxy rule that Validate refuses.
//
// A host that is an IP address is refused, unless allowed holds it. Each
// range of allowed must pass egress.CheckAllowedRange, and the address must
// pass the outbound guard with those ranges.
func Parse(doc []byte, allowed ...netip.Prefix) (RuleSet, error) {
	if len(doc) > MaxDocumentBytes {
		return RuleSet{}, fmt.Errorf("%w: %s", ErrInvalid, reasonDocumentSize)
	}

	for _, p := range allowed {
		if err := egress.CheckAllowedRange(p); err != nil {
			return RuleSet{}, fmt.Errorf("%w: %s", ErrInvalid, reasonAllowedRange)
		}
	}

	if err := checkKeys(doc); err != nil {
		return RuleSet{}, err
	}

	var d document

	dec := json.NewDecoder(bytes.NewReader(doc))
	dec.DisallowUnknownFields()

	if err := dec.Decode(&d); err != nil || d.Rules == nil {
		return RuleSet{}, fmt.Errorf("%w: %s", ErrInvalid, reasonDocumentShape)
	}

	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return RuleSet{}, fmt.Errorf("%w: %s", ErrInvalid, reasonDocumentShape)
	}

	if len(*d.Rules) > MaxRules {
		return RuleSet{}, fmt.Errorf("%w: %s", ErrInvalid, reasonTooManyRules)
	}

	set := RuleSet{Rules: make([]Rule, 0, len(*d.Rules))}

	for i := range *d.Rules {
		set.Rules = append(set.Rules, (*d.Rules)[i].rule())
	}

	if err := set.checkWith(func(netip.Addr) []netip.Prefix { return allowed }); err != nil {
		return RuleSet{}, err
	}

	return set, nil
}

// rule returns the proxy rule of dr with its effective port and server
// trust.
func (dr *documentRule) rule() Rule {
	port := DefaultPort
	if dr.Port != nil {
		port = *dr.Port
	}

	trust := dr.ServerTrust
	if trust == "" {
		trust = ServerTrustSystem
	}

	return Rule{
		ID:                dr.ID,
		Operations:        dr.Operations,
		Host:              dr.Host,
		Port:              port,
		Mode:              dr.Mode,
		Methods:           dr.Methods,
		Paths:             dr.Paths,
		Credential:        dr.Credential,
		ClientCertificate: dr.ClientCertificate,
		ServerTrust:       trust,
	}
}

// Validate checks each proxy rule of s, that each ID is used once, and that
// one host and port have one mode. Render and Join run it too, so a proxy
// rule set that a caller builds by hand meets the same checks as one that Parse
// returns. It does not check MaxRules, because a joined set can be larger.
//
// Validate knows no allowed range. It accepts a host that is an IP address
// only in its canonical form and only when an allowed range could open it:
// a private-network address that the outbound guard opens with a range of
// that address alone. The dial applies the outbound guard again with the
// ranges of the runner pool.
func (s RuleSet) Validate() error {
	return s.checkWith(rangeOfAddress)
}

// rangesFor returns the allowed ranges that an IP address host must be
// inside.
type rangesFor func(addr netip.Addr) []netip.Prefix

// rangeOfAddress gives the range of the address alone, the narrowest range
// that a runner pool could allow for it.
func rangeOfAddress(addr netip.Addr) []netip.Prefix {
	return []netip.Prefix{netip.PrefixFrom(addr, addr.BitLen())}
}

// checkWith checks s as Validate does, with ranges for an IP address host.
func (s RuleSet) checkWith(ranges rangesFor) error {
	ids := make(map[string]bool, len(s.Rules))
	modes := make(map[string]Mode, len(s.Rules))

	for i := range s.Rules {
		r := &s.Rules[i]

		if reason := r.check(ranges); reason != "" {
			return ruleError(i, r, reason)
		}

		if ids[r.ID] {
			return ruleError(i, r, reasonIDTwice)
		}

		ids[r.ID] = true

		key := hostPort(r.Host, r.Port)
		if m, ok := modes[key]; ok && m != r.Mode {
			return ruleError(i, r, reasonModeTwice)
		}

		modes[key] = r.Mode
	}

	return nil
}

// ruleError names the proxy rule by its position and, when it is valid, its
// ID. It never holds another value of the proxy rule.
func ruleError(i int, r *Rule, reason string) error {
	if idPattern.MatchString(r.ID) {
		return fmt.Errorf("%w: proxy rule %d (%s): %s", ErrInvalid, i+1, r.ID, reason)
	}

	return fmt.Errorf("%w: proxy rule %d: %s", ErrInvalid, i+1, reason)
}

// hostPort is the key of a host and a port.
func hostPort(host string, port int) string {
	return host + ":" + strconv.Itoa(port)
}

// check returns the fixed reason that r is refused, or "".
func (r *Rule) check(ranges rangesFor) string {
	if reason := r.checkFields(); reason != "" {
		return reason
	}

	if reason := checkHost(r.Host, ranges); reason != "" {
		return reason
	}

	if !r.Mode.checksRequests() {
		// A tunnel does not end TLS, so each of these fields would have no
		// effect. A field with no effect is refused, not ignored.
		if len(r.Methods) > 0 || len(r.Paths) > 0 || r.Credential != "" || r.ClientCertificate != "" ||
			r.ServerTrust != ServerTrustSystem {
			return reasonTunnelRequest
		}

		return ""
	}

	return r.checkRequests()
}

// checkFields checks the fields of r that each mode has.
func (r *Rule) checkFields() string {
	switch {
	case !idPattern.MatchString(r.ID):
		return reasonID
	case !validOperations(r.Operations):
		return reasonOperations
	case r.Port < 1 || r.Port > maxPort:
		return reasonPort
	case !r.Mode.Valid():
		return reasonMode
	case !validTrust(r.ServerTrust):
		return reasonServerTrust
	case !validExport(r.Credential) || !validExport(r.ClientCertificate):
		return reasonExportName
	default:
		return ""
	}
}

// checkRequests checks the methods and the paths of an inspect or endpoint
// proxy rule.
func (r *Rule) checkRequests() string {
	if !validMethods(r.Methods) {
		return reasonMethods
	}

	if len(r.Paths) == 0 || len(r.Paths) > MaxPaths || hasDuplicate(r.Paths) {
		return reasonPaths
	}

	for _, p := range r.Paths {
		if _, reason := parsePattern(p); reason != "" {
			return reason
		}
	}

	return ""
}

// validOperations reports whether ops lists known operations, each once.
func validOperations(ops []Operation) bool {
	if len(ops) == 0 || hasDuplicate(ops) {
		return false
	}

	for _, o := range ops {
		if !o.Valid() {
			return false
		}
	}

	return true
}

// validMethods reports whether ms lists allowed methods, each once.
func validMethods(ms []string) bool {
	if len(ms) == 0 || hasDuplicate(ms) {
		return false
	}

	allowed := methods()

	for _, m := range ms {
		if !slices.Contains(allowed, m) {
			return false
		}
	}

	return true
}

// validExport reports whether name is empty or a valid export name.
func validExport(name string) bool {
	return name == "" || namePattern.MatchString(name)
}

// validTrust reports whether t is ServerTrustSystem or a CA export name.
func validTrust(t string) bool {
	return t == ServerTrustSystem || namePattern.MatchString(t)
}

// hasDuplicate reports whether list holds a value twice.
func hasDuplicate[T comparable](list []T) bool {
	seen := make(map[T]bool, len(list))

	for _, v := range list {
		if seen[v] {
			return true
		}

		seen[v] = true
	}

	return false
}

// checkHost returns the fixed reason that host is refused, or "". The host
// is never normalized: an uppercase letter or a dot at the end is refused.
func checkHost(host string, ranges rangesFor) string {
	if addr, err := netip.ParseAddr(host); err == nil {
		return checkIPHost(host, addr, ranges(addr))
	}

	if !validDNSName(host) {
		return reasonHost
	}

	return ""
}

// checkIPHost checks a host that is an IP address. It passes only as a
// private-network address in its canonical form, inside a range of allowed,
// and through the outbound guard with those ranges.
func checkIPHost(host string, addr netip.Addr, allowed []netip.Prefix) string {
	inRange := slices.ContainsFunc(allowed, func(p netip.Prefix) bool { return p.Contains(addr) })

	// Defense in depth: the range check above refuses an address outside
	// each allowed range; the outbound guard stays so that a range that opens
	// a never-opened class (loopback, metadata) still cannot pass.
	guard := egress.Rules{Allow: allowed, LoopbackForTests: false}.CheckAddr(addr)

	// An allowed range opens private-network addresses only, so a public
	// address is never a host of a proxy rule: a DNS name names it.
	if !inRange || !addr.IsPrivate() || guard != nil || addr.Zone() != "" || addr.String() != host {
		return reasonHostIP
	}

	return ""
}

// validDNSName reports whether host is an exact DNS name in lowercase with
// two or more labels, no dot at the end and a last label that is not a
// number.
func validDNSName(host string) bool {
	if host == "" || len(host) > MaxHostBytes {
		return false
	}

	labels := strings.Split(host, ".")
	if len(labels) < minLabels {
		return false
	}

	for _, l := range labels {
		if !validLabel(l) {
			return false
		}
	}

	return !numericLabel(labels[len(labels)-1])
}

// validLabel reports whether l is one lowercase DNS label.
func validLabel(l string) bool {
	if l == "" || len(l) > maxLabelBytes || l[0] == '-' || l[len(l)-1] == '-' {
		return false
	}

	for i := range len(l) {
		c := l[i]
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
			return false
		}
	}

	return true
}

// numericLabel reports whether l holds digits only, so a name such as
// "127.1" is not read as a DNS name.
func numericLabel(l string) bool {
	return strings.Trim(l, "0123456789") == ""
}

// knownKeys returns the exact spelling of each key of a proxy rule set
// document.
func knownKeys() []string {
	return []string{
		"rules", "id", "operations", "host", "port", "mode", "methods", "paths",
		"credential", "clientCertificate", "serverTrust",
	}
}

// checkKeys walks the JSON tokens of doc and refuses a key that one object
// holds twice. encoding/json keeps the last value of a key, so a reviewer
// could read one value and the job proxy apply another.
func checkKeys(doc []byte) error {
	dec := json.NewDecoder(bytes.NewReader(doc))

	return walkValue(dec, 0)
}

// walkValue reads one JSON value from dec and checks the keys of each
// object in it.
func walkValue(dec *json.Decoder, depth int) error {
	if depth > maxDepth {
		return fmt.Errorf("%w: %s", ErrInvalid, reasonDocumentTooDeep)
	}

	tok, err := dec.Token()
	if err != nil {
		return fmt.Errorf("%w: %s", ErrInvalid, reasonDocumentShape)
	}

	delim, ok := tok.(json.Delim)
	if !ok {
		return nil
	}

	switch delim {
	case '{':
		return walkObject(dec, depth)
	case '[':
		for dec.More() {
			if err := walkValue(dec, depth+1); err != nil {
				return err
			}
		}

		return closing(dec)
	default:
		return fmt.Errorf("%w: %s", ErrInvalid, reasonDocumentShape)
	}
}

// walkObject reads the keys and values of one object after its "{".
func walkObject(dec *json.Decoder, depth int) error {
	seen := map[string]bool{}

	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return fmt.Errorf("%w: %s", ErrInvalid, reasonDocumentShape)
		}

		key, ok := tok.(string)
		if !ok {
			return fmt.Errorf("%w: %s", ErrInvalid, reasonDocumentShape)
		}

		if seen[key] {
			return fmt.Errorf("%w: %s", ErrInvalid, reasonDuplicateKey)
		}

		// encoding/json matches a key with no regard to case, so "Host" would
		// fill the field host. Only the exact spelling of a known key passes.
		// Defense in depth: the decoder refuses unknown fields too; this check
		// stays so that a second spelling of a key cannot pass it.
		if !slices.Contains(knownKeys(), key) {
			return fmt.Errorf("%w: %s", ErrInvalid, reasonDocumentShape)
		}

		seen[key] = true

		if err := walkValue(dec, depth+1); err != nil {
			return err
		}
	}

	return closing(dec)
}

// closing reads the "}" or "]" that ends an object or a list.
func closing(dec *json.Decoder) error {
	if _, err := dec.Token(); err != nil {
		return fmt.Errorf("%w: %s", ErrInvalid, reasonDocumentShape)
	}

	return nil
}
