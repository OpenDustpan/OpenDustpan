package profile

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Keys of exports.
const (
	exportVariables = "variables"
	exportFiles     = "files"
	exportPulumi    = "pulumi"
	exportOpenTofu  = "opentofu"
	exportProxy     = "proxy"
)

// Consumer names what reads the exports of a profile. Each consumer gets
// only the export keys of its own list (see ExportsFor).
type Consumer string

const (
	// ConsumerLauncher is the launcher of a run command or of a step. It
	// gets variables, files, pulumi and opentofu, and never proxy.
	ConsumerLauncher Consumer = "launcher"
	// ConsumerJobProxy is the job proxy next to a job. It gets proxy only.
	ConsumerJobProxy Consumer = "job-proxy"
)

// consumerKeys returns the allowlist of export keys of consumer. A
// consumer that is not known gets nothing and false.
func consumerKeys(consumer Consumer) ([]string, bool) {
	switch consumer {
	case ConsumerLauncher:
		return []string{exportVariables, exportFiles, exportPulumi, exportOpenTofu}, true
	case ConsumerJobProxy:
		return []string{exportProxy}, true
	default:
		return nil, false
	}
}

// ProxyType is the kind of one credential of exports.proxy.
type ProxyType string

// The kinds of a credential of exports.proxy.
const (
	ProxyHeader            ProxyType = "header"
	ProxyBasic             ProxyType = "basic"
	ProxyQuery             ProxyType = "query"
	ProxyClientCertificate ProxyType = "client-certificate"
	ProxyCA                ProxyType = "ca"
)

// Fields of a credential of exports.proxy.
const (
	proxyFieldType         = "type"
	proxyFieldName         = "name"
	proxyFieldValue        = "value"
	proxyFieldUser         = "user"
	proxyFieldPassword     = "password"
	proxyFieldCertificate  = "certificate"
	proxyFieldKey          = "key"
	proxyFieldCertificates = "certificates"
)

// pathSegments is one or more segments of a path: ".name", ["name"] or
// ['name'].
const pathSegments = `(?:\.` + identifier + `|\[\s*"[^"\\]*"\s*\]|\[\s*'[^'\\]*'\s*\])+`

// Limits of exports.proxy.
const (
	// MaxProxyExports is the most credentials that exports.proxy holds.
	MaxProxyExports = 64
	// maxProxyNameBytes is the longest name of a header or of a query
	// parameter.
	maxProxyNameBytes = 128
)

// proxyFieldKind says how the parser reads one field of a credential.
type proxyFieldKind int

const (
	// fieldName is literal text: a header name or a query parameter name.
	fieldName proxyFieldKind = iota
	// fieldRef is a reference into values that may hold a plain value.
	fieldRef
	// fieldSecretRef is a reference into values that must hold a secret.
	fieldSecretRef
)

// proxyFields returns the fields of each kind of credential. Each field is
// required, and no other field is allowed. An unknown kind returns false.
func proxyFields(t ProxyType) (map[string]proxyFieldKind, bool) {
	switch t {
	case ProxyHeader, ProxyQuery:
		return map[string]proxyFieldKind{proxyFieldName: fieldName, proxyFieldValue: fieldSecretRef}, true
	case ProxyBasic:
		return map[string]proxyFieldKind{proxyFieldUser: fieldRef, proxyFieldPassword: fieldSecretRef}, true
	case ProxyClientCertificate:
		return map[string]proxyFieldKind{proxyFieldCertificate: fieldRef, proxyFieldKey: fieldSecretRef}, true
	case ProxyCA:
		return map[string]proxyFieldKind{proxyFieldCertificates: fieldRef}, true
	default:
		return nil, false
	}
}

var (
	// proxyExportName is the allowlist of the name of a credential.
	proxyExportName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)
	// headerName is the allowlist of a header name: the token characters of
	// HTTP.
	headerName = regexp.MustCompile("^[A-Za-z0-9!#$%&'*+.^_`|~-]+$")
	// queryName is the allowlist of a query parameter name: the characters
	// that a URL never encodes.
	queryName = regexp.MustCompile(`^[A-Za-z0-9._~-]+$`)
	// soleSecretRef is a string that is exactly one reference to a value:
	// a path under values (group 1), an output of a provider or a rotator, or
	// a value of another profile. Any other shape (an operator, a function,
	// a literal, text around the reference) does not match.
	soleSecretRef = regexp.MustCompile(`^\$\{\s*(?:(values` + pathSegments + `)|(?:providers|rotators)` +
		pathSegments + `|(?:imports|profiles)\[\s*"[^"\\]*"\s*\]\.values` + pathSegments + `)\s*\}$`)
	// soleValuesRef is a string that is exactly one reference into values.
	soleValuesRef = regexp.MustCompile(`^\$\{\s*(values` + pathSegments + `)\s*\}$`)
)

// isForbiddenHeader reports whether name is a header that frames or routes
// a request. A credential never sets such a header, because the job proxy
// must keep it as it checked it.
func isForbiddenHeader(name string) bool {
	switch strings.ToLower(name) {
	case "connection", "content-length", "host", "keep-alive", "proxy-authenticate", "proxy-authorization",
		"proxy-connection", "te", "trailer", "transfer-encoding", "upgrade":
		return true
	default:
		return false
	}
}

// ProxyExport is one credential of exports.proxy, as written. Each
// reference field holds the reference text, for example
// "${ values.grafana.token }". The open evaluates it.
type ProxyExport struct {
	Type ProxyType
	// Name is the header name or the query parameter name.
	Name         string
	Value        string
	User         string
	Password     string
	Certificate  string
	Key          string
	Certificates string
}

// Exports holds the export keys that one consumer gets, keyed by the export
// key. The value of proxy is a map[string]ProxyExport. The value of each
// other key is the decoded YAML, with each expression as text.
type Exports map[string]any

// ExportsFor returns only the exports of doc that consumer may get. The job
// proxy gets proxy only. The launcher never gets proxy. An unknown consumer
// or a nil document returns ErrInvalid and no exports. Each delivery of the
// exports of a profile must call this function.
func ExportsFor(doc *Document, consumer Consumer) (Exports, error) {
	allowed, ok := consumerKeys(consumer)
	if !ok {
		return nil, fmt.Errorf("%w: unknown consumer %q of exports", ErrInvalid, consumer)
	}

	if doc == nil || doc.root == nil {
		return nil, fmt.Errorf("%w: no document", ErrInvalid)
	}

	out := Exports{}

	exports := mapGet(doc.root, topExports)
	if exports == nil {
		return out, nil
	}

	for i := 0; i+1 < len(exports.Content); i += pairSize {
		key, val := exports.Content[i].Value, exports.Content[i+1]

		if !slices.Contains(allowed, key) || !proxyOnlyForJobProxy(consumer, key) {
			continue
		}

		got, err := decodeExport(doc, key, val)
		if err != nil {
			return nil, err
		}

		out[key] = got
	}

	return out, nil
}

// proxyOnlyForJobProxy reports whether consumer may get the export key.
// It refuses proxy to each consumer but the job proxy.
//
// Defense in depth: the allowlist of each consumer filters the keys too.
// This check stays so that a later edit of the allowlist cannot give proxy
// to the launcher or to a new consumer.
func proxyOnlyForJobProxy(consumer Consumer, key string) bool {
	return key != exportProxy || consumer == ConsumerJobProxy
}

// decodeExport decodes the export key with the node val.
func decodeExport(doc *Document, key string, val *yaml.Node) (any, error) {
	if key == exportProxy {
		return parseProxy(doc, val)
	}

	var v any
	if err := val.Decode(&v); err != nil {
		return nil, fmt.Errorf("%w: exports.%s: %w", ErrInvalid, key, err)
	}

	return v, nil
}

// checkExports checks the exports map n of the document d.
func (d *Document) checkExports(n *yaml.Node) error {
	if n == nil {
		return nil
	}

	if n.Kind != yaml.MappingNode {
		return fmt.Errorf("%w: exports must be a map", ErrInvalid)
	}

	for i := 0; i+1 < len(n.Content); i += pairSize {
		switch n.Content[i].Value {
		case exportVariables, exportFiles, exportPulumi, exportOpenTofu:
		case exportProxy:
			if _, err := parseProxy(d, n.Content[i+1]); err != nil {
				return err
			}
		default:
			return fmt.Errorf("%w: unknown key %q in exports", ErrInvalid, n.Content[i].Value)
		}
	}

	return nil
}

// parseProxy reads and checks exports.proxy.
func parseProxy(d *Document, n *yaml.Node) (map[string]ProxyExport, error) {
	if n.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("%w: exports.proxy must be a map of credentials", ErrInvalid)
	}

	if len(n.Content)/pairSize > MaxProxyExports {
		return nil, fmt.Errorf("%w: exports.proxy holds more than %d credentials", ErrLimit, MaxProxyExports)
	}

	out := make(map[string]ProxyExport, len(n.Content)/pairSize)

	for i := 0; i+1 < len(n.Content); i += pairSize {
		name := n.Content[i].Value
		if !proxyExportName.MatchString(name) {
			return nil, fmt.Errorf("%w: exports.proxy: %q is not a credential name: use 1 to 63 of a-z, 0-9, "+
				"\"_\" and \"-\", and start with a letter or a digit", ErrInvalid, truncate(name))
		}

		e, err := parseProxyExport(d, name, n.Content[i+1])
		if err != nil {
			return nil, err
		}

		out[name] = e
	}

	return out, nil
}

// parseProxyExport reads and checks one credential of exports.proxy.
func parseProxyExport(d *Document, name string, n *yaml.Node) (ProxyExport, error) {
	path := "exports.proxy." + name

	if n.Kind != yaml.MappingNode {
		return ProxyExport{}, fmt.Errorf("%w: %s must be a map with a %q", ErrInvalid, path, proxyFieldType)
	}

	t := mapGet(n, proxyFieldType)
	if t == nil || t.Kind != yaml.ScalarNode || t.Tag != tagStr {
		return ProxyExport{}, fmt.Errorf("%w: %s needs a %q", ErrInvalid, path, proxyFieldType)
	}

	fields, ok := proxyFields(ProxyType(t.Value))
	if !ok {
		return ProxyExport{}, fmt.Errorf("%w: %s: type %q is not one of header, basic, query, "+
			"client-certificate and ca", ErrInvalid, path, truncate(t.Value))
	}

	e := ProxyExport{
		Type: ProxyType(t.Value), Name: "", Value: "", User: "", Password: "", Certificate: "", Key: "",
		Certificates: "",
	}

	for i := 0; i+1 < len(n.Content); i += pairSize {
		k := n.Content[i].Value
		if k == proxyFieldType {
			continue
		}

		kind, known := fields[k]
		if !known {
			return ProxyExport{}, fmt.Errorf(
				"%w: %s: %q is not a field of type %s",
				ErrInvalid,
				path,
				truncate(k),
				e.Type,
			)
		}

		text, err := d.proxyField(path+"."+k, e.Type, kind, n.Content[i+1])
		if err != nil {
			return ProxyExport{}, err
		}

		e.set(k, text)
	}

	for _, k := range slices.Sorted(maps.Keys(fields)) {
		if mapGet(n, k) == nil {
			return ProxyExport{}, fmt.Errorf("%w: %s: type %s needs the field %q", ErrInvalid, path, e.Type, k)
		}
	}

	return e, nil
}

// set stores the text of the field k.
func (e *ProxyExport) set(k, text string) {
	switch k {
	case proxyFieldName:
		e.Name = text
	case proxyFieldValue:
		e.Value = text
	case proxyFieldUser:
		e.User = text
	case proxyFieldPassword:
		e.Password = text
	case proxyFieldCertificate:
		e.Certificate = text
	case proxyFieldKey:
		e.Key = text
	case proxyFieldCertificates:
		e.Certificates = text
	default:
		// parseProxyExport refuses each other field before it calls set.
	}
}

// proxyField checks one field of a credential and returns its text.
func (d *Document) proxyField(path string, t ProxyType, kind proxyFieldKind, n *yaml.Node) (string, error) {
	if n.Kind != yaml.ScalarNode || n.Tag != tagStr {
		return "", fmt.Errorf("%w: %s must be text", ErrInvalid, path)
	}

	if kind == fieldName {
		return n.Value, checkProxyName(path, t, n.Value)
	}

	m := soleValuesRef.FindStringSubmatch(n.Value)
	if m == nil {
		return "", fmt.Errorf("%w: %s must be one reference into values, for example "+
			"${ values.<path> }: put the secret in values", ErrInvalid, path)
	}

	refs := refsInText(n.Value)
	if len(refs) != 1 {
		return "", fmt.Errorf("%w: %s must be one reference into values", ErrInvalid, path)
	}

	if err := d.checkTarget(path, t, kind, refs[0]); err != nil {
		return "", err
	}

	return n.Value, nil
}

// checkProxyName checks a header name or a query parameter name.
func checkProxyName(path string, t ProxyType, name string) error {
	if name == "" || len(name) > maxProxyNameBytes {
		return fmt.Errorf("%w: %s must have 1 to %d bytes", ErrInvalid, path, maxProxyNameBytes)
	}

	if t == ProxyQuery {
		if !queryName.MatchString(name) {
			return fmt.Errorf("%w: %s holds a character that is not a-z, A-Z, 0-9, \".\", \"_\", \"~\" or \"-\"",
				ErrInvalid, path)
		}

		return nil
	}

	if !headerName.MatchString(name) {
		return fmt.Errorf("%w: %s is not a header name", ErrInvalid, path)
	}

	if isForbiddenHeader(name) {
		return fmt.Errorf("%w: %s: a credential cannot set the header %q", ErrInvalid, path, name)
	}

	return nil
}

// checkTarget checks the node of values that a reference names. A path
// that the document does not hold passes only when the document has
// imports, because only an import can give it.
func (d *Document) checkTarget(path string, t ProxyType, kind proxyFieldKind, ref []string) error {
	if len(ref) == 0 {
		return fmt.Errorf("%w: %s must name a path under values", ErrInvalid, path)
	}

	target := d.nodeAt(ref)
	if target == nil {
		if d.hasImports() {
			return nil
		}

		return fmt.Errorf("%w: %s reads a path that is not in values, and the profile has no imports",
			ErrInvalid, path)
	}

	if target.Kind != yaml.ScalarNode && !isEntryMap(target) {
		return fmt.Errorf("%w: %s must read one value, not a map or a list", ErrInvalid, path)
	}

	// The reference ends inside an entry or a plain value: nodeAt stopped
	// early, so the path is not a leaf of values.
	if d.nodeAt(ref[:len(ref)-1]) == target {
		return fmt.Errorf("%w: %s reads a path inside a value", ErrInvalid, path)
	}

	if kind == fieldSecretRef {
		if err := d.checkSecretTarget(path, target); err != nil {
			return err
		}
	}

	return checkTargetText(path, t, target)
}

// checkSecretTarget refuses the target of a secret field unless it is a
// secret source or a chain of sole references to one (see readsSecret). A
// literal, a plain value and each computed expression fail, so a static key
// never sits in plain text in a document.
func (d *Document) checkSecretTarget(path string, target *yaml.Node) error {
	if d.readsSecret(target, map[*yaml.Node]bool{}, 0) {
		return nil
	}

	return fmt.Errorf("%w: %s must read a %s value, a provider or rotator output, or a value of another profile, "+
		"through references only",
		ErrInvalid, path, keySecret)
}

// readsSecret reports whether the node n of values is a secret source, or
// is exactly one reference to one. A secret source is a $secret or $sealed
// entry, an output of a provider or a rotator, a value of another profile
// (imports[...] or profiles[...]), or a path under values that the document
// does not hold when the document has imports. Each step of the chain must
// be a $secret or $sealed entry, or a value whose whole text is one
// reference (soleSecretRef). Each other shape is false, so an unknown case
// fails closed.
//
// This check cannot see a value that another profile gives, so it does not
// check that such a value is a secret. No check at the open exists yet.
func (d *Document) readsSecret(n *yaml.Node, seen map[*yaml.Node]bool, depth int) bool {
	if n == nil || seen[n] || depth > MaxDepth {
		return false
	}

	seen[n] = true

	if !isEntryMap(n) {
		return d.refReadsSecret(n, seen, depth)
	}

	if mapGet(n, keySecret) != nil || mapGet(n, keySealed) != nil {
		return true
	}

	v := mapGet(n, keyValue)

	return v != nil && d.refReadsSecret(v, seen, depth)
}

// refReadsSecret reports whether the node n is text that is exactly one
// reference to a secret source.
func (d *Document) refReadsSecret(n *yaml.Node, seen map[*yaml.Node]bool, depth int) bool {
	if n.Kind != yaml.ScalarNode || n.Tag != tagStr {
		return false
	}

	m := soleSecretRef.FindStringSubmatch(n.Value)
	if m == nil {
		return false
	}

	if m[1] == "" {
		// An output of a provider or a rotator, or a value of another
		// profile.
		return true
	}

	var ref []string

	for _, seg := range pathSegment.FindAllStringSubmatch(strings.TrimPrefix(m[1], topValues), -1) {
		ref = append(ref, seg[1]+seg[2]+seg[3])
	}

	target := d.nodeAt(ref)
	if target == nil {
		return d.hasImports()
	}

	// The reference ends inside an entry or a plain value.
	if len(ref) == 0 || d.nodeAt(ref[:len(ref)-1]) == target {
		return false
	}

	return d.readsSecret(target, seen, depth+1)
}

// hasImports reports whether the document imports at least one profile.
func (d *Document) hasImports() bool {
	imports := mapGet(d.root, topImports)

	return imports != nil && imports.Kind == yaml.SequenceNode && len(imports.Content) > 0
}

// checkTargetText refuses a control character in the plain text of a value
// that goes into a header or into the user of Basic auth.
func checkTargetText(path string, t ProxyType, target *yaml.Node) error {
	if t != ProxyHeader && t != ProxyBasic {
		return nil
	}

	text := target.Value
	if isEntryMap(target) {
		text = ""

		for _, k := range []string{keySecret, keyValue} {
			if v := mapGet(target, k); v != nil && v.Kind == yaml.ScalarNode {
				text = v.Value
			}
		}
	}

	if strings.ContainsFunc(text, isControl) {
		return fmt.Errorf("%w: %s reads a value with a control character, such as CR or LF", ErrInvalid, path)
	}

	if t == ProxyBasic && strings.HasSuffix(path, "."+proxyFieldUser) && strings.Contains(text, ":") {
		return fmt.Errorf("%w: %s reads a user with a \":\"", ErrInvalid, path)
	}

	return nil
}

func isControl(r rune) bool { return r < 0x20 || r == 0x7f }

// truncate shortens text for an error message.
func truncate(s string) string {
	const maxText = 64
	if len(s) <= maxText {
		return s
	}

	return s[:maxText] + "..."
}
