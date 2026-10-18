package declaration

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"opendustpan.org/providerkit/cliexec"
	"opendustpan.org/providerkit/schema"
)

// slot is where a template goes, which decides the references it may hold.
type slot uint8

const (
	// slotPublic is a place that a process list, a proxy log or a shell
	// history can show: a command argument, an environment variable, a URL
	// path or a query. It takes no sensitive value.
	slotPublic slot = iota
	// slotPrivate is a request body or the standard input of a command.
	slotPrivate
)

// scope is what the references of one template can name.
type scope struct {
	attrs  schema.Schema
	config schema.Schema
	cli    bool
	// onlyID limits the attribute references to "{id}", for the read of a
	// resource: an import has only the id.
	onlyID bool
	// noWriteOnly refuses a reference to a write-only attribute, for the
	// delete of a resource: state does not hold its value.
	noWriteOnly bool
}

func (sc scope) check(text string, sl slot) error {
	t, err := ParseTemplate(text)
	if err != nil {
		return err
	}

	for _, ref := range t.Refs() {
		if err := sc.checkRef(ref, sl); err != nil {
			return err
		}
	}

	return nil
}

func (sc scope) checkRef(ref Ref, sl slot) error {
	a, err := sc.lookup(ref)
	if err != nil {
		return err
	}

	if err := sc.checkState(ref, a); err != nil {
		return err
	}

	switch {
	case ref.Kind == RefFile && (!sc.cli || sl != slotPublic):
		return fmt.Errorf("%w: %s is allowed only in a command argument or an environment variable", ErrInvalid, ref)
	case ref.Kind != RefFile && (a.Sensitive || a.WriteOnly) && sl == slotPublic:
		return fmt.Errorf(
			"%w: %s is sensitive or write-only and cannot go in an argument, a variable, a path or a query",
			ErrInvalid,
			ref,
		)
	default:
		return nil
	}
}

// checkState checks a reference to the attribute a against what the
// operation has: the read of a resource has only the id, and the delete of a
// resource has only state, which holds no write-only value.
func (sc scope) checkState(ref Ref, a schema.Attribute) error {
	switch {
	case ref.Kind != RefAttribute:
		return nil
	case sc.onlyID && ref.Name != schema.IDAttribute:
		return fmt.Errorf("%w: %s: the read of a resource can reference only {%s} and the configuration, "+
			"because an import gives only the id", ErrInvalid, ref, schema.IDAttribute)
	case sc.noWriteOnly && a.WriteOnly:
		return fmt.Errorf("%w: %s is write-only, and state does not hold its value for a delete", ErrInvalid, ref)
	default:
		return nil
	}
}

// lookup returns the attribute that the reference names.
func (sc scope) lookup(ref Ref) (schema.Attribute, error) {
	var (
		a  schema.Attribute
		ok bool
	)

	switch ref.Kind {
	case RefAttribute:
		a, ok = sc.attrs.Lookup(ref.Name)
	case RefConfig, RefFile:
		a, ok = sc.config.Lookup(ref.Name)
	case RefNone:
		return a, fmt.Errorf("%w: a literal is not a reference", ErrInvalid)
	default:
		return a, fmt.Errorf("%w: unknown reference kind %d", ErrInvalid, ref.Kind)
	}

	if !ok {
		return a, fmt.Errorf("%w: %s names no attribute", ErrInvalid, ref)
	}

	return a, nil
}

func (d Document) validateResource(r Resource, config schema.Schema) error {
	s, err := r.Schema()
	if err != nil {
		return err
	}

	if err := s.ValidateResource(); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	if err := needsUpdate(r, s); err != nil {
		return err
	}

	sc := scope{attrs: s, config: config, cli: d.CLI != nil, onlyID: false, noWriteOnly: false}
	for _, o := range []struct {
		name string
		op   *Operation
	}{
		{name: "create", op: &r.Create},
		{name: "read", op: &r.Read},
		{name: "update", op: r.Update},
		{name: "delete", op: &r.Delete},
	} {
		if o.op == nil {
			continue
		}

		sc.onlyID = o.name == "read"
		sc.noWriteOnly = o.name == "delete"
		if err := sc.operation(*o.op); err != nil {
			return fmt.Errorf("%s: %w", o.name, err)
		}
	}

	return checkRemoteSet(r, s)
}

// checkRemoteSet checks that each operation gives what the remote system
// sets:
//   - Create gives a computed id.
//   - Read gives each computed attribute other than the id, because an
//     import has only the id and Read.
//   - Create or read gives each computed or optional computed attribute.
//   - When the resource has update, update or read gives each computed or
//     optional computed attribute that is not stable and not the id. The
//     plan of an update makes those values unknown, and the apply must give
//     them.
func checkRemoteSet(r Resource, s schema.Schema) error {
	if id, _ := s.Lookup(schema.IDAttribute); id.Mode == schema.ModeComputed && !r.Create.maps(schema.IDAttribute) {
		return fmt.Errorf("%w: create: the reply must give %q", ErrInvalid, schema.IDAttribute)
	}

	for _, a := range s.Attributes {
		if setByRemote(a) && a.Name != schema.IDAttribute {
			if err := r.checkGiven(a); err != nil {
				return err
			}
		}
	}

	return nil
}

// checkGiven checks that the operations give the attribute a, which the
// remote system sets and which is not the id.
func (r Resource) checkGiven(a schema.Attribute) error {
	switch {
	case a.Mode == schema.ModeComputed && !r.Read.maps(a.Name):
		return fmt.Errorf("%w: read must give the computed attribute %q, which an import needs",
			ErrInvalid, a.Name)
	case !r.Create.maps(a.Name) && !r.Read.maps(a.Name):
		return fmt.Errorf("%w: create or read must give the attribute %q, which the remote system sets",
			ErrInvalid, a.Name)
	case r.Update != nil && !a.Stable && !r.Update.maps(a.Name) && !r.Read.maps(a.Name):
		return fmt.Errorf("%w: update or read must give the attribute %q, which an update makes unknown",
			ErrInvalid, a.Name)
	default:
		return nil
	}
}

// setByRemote reports whether the remote system can set the attribute.
func setByRemote(a schema.Attribute) bool {
	return a.Mode == schema.ModeComputed || a.Mode == schema.ModeOptionalComputed
}

// ReadsAfterCreate reports whether the engine runs Read after Create: the
// reply or output of create does not give every attribute that the remote
// system sets. The values of the apply then have no unknown value.
func (r Resource) ReadsAfterCreate() bool {
	return r.misses(r.Create, func(Attribute) bool { return false })
}

// ReadsAfterUpdate reports whether the engine runs Read after Update: the
// reply or output of update does not give every attribute that the remote
// system sets and that is not stable or the id. The plan of an update makes
// those values unknown.
func (r Resource) ReadsAfterUpdate() bool {
	return r.Update != nil &&
		r.misses(*r.Update, func(a Attribute) bool { return a.Stable || a.Name == schema.IDAttribute })
}

// misses reports whether the operation does not give an attribute that the
// remote system sets and that skip does not exclude.
func (r Resource) misses(o Operation, skip func(Attribute) bool) bool {
	for _, a := range r.Attributes {
		mode := parseMode(a.Mode)
		if mode != schema.ModeComputed && mode != schema.ModeOptionalComputed || skip(a) {
			continue
		}

		if !o.maps(a.Name) {
			return true
		}
	}

	return false
}

// needsUpdate checks that a resource without an update replaces the object
// on every change that the user can make.
func needsUpdate(r Resource, s schema.Schema) error {
	if r.Update != nil {
		return nil
	}

	for _, a := range s.Attributes {
		if a.Mode.UserSets() && !a.WriteOnly && !a.Replace {
			return fmt.Errorf(
				"%w: attribute %q needs %q, or the resource needs %q",
				ErrInvalid,
				a.Name,
				"replace",
				"update",
			)
		}
	}

	return nil
}

func (d Document) validateDataSource(ds DataSource, config schema.Schema) error {
	s, err := ds.Schema()
	if err != nil {
		return err
	}

	if err := s.ValidateDataSource(); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	sc := scope{attrs: s, config: config, cli: d.CLI != nil, onlyID: false, noWriteOnly: false}
	if err := sc.operation(ds.Read); err != nil {
		return fmt.Errorf("read: %w", err)
	}

	for _, a := range s.Attributes {
		if a.Mode == schema.ModeComputed && !ds.Read.maps(a.Name) {
			return fmt.Errorf("%w: read: the reply must give the computed attribute %q", ErrInvalid, a.Name)
		}
	}

	return nil
}

// maps reports whether the operation gives a value for the attribute.
func (o Operation) maps(name string) bool {
	switch {
	case o.Request != nil:
		_, ok := o.Request.Response[name]

		return ok
	case o.Command != nil:
		_, ok := o.Command.Output[name]

		return ok
	default:
		return false
	}
}

func (sc scope) operation(o Operation) error {
	switch {
	case !sc.cli && o.Request != nil && o.Command == nil:
		return sc.request(*o.Request)
	case sc.cli && o.Command != nil && o.Request == nil:
		return sc.command(*o.Command)
	case sc.cli:
		return fmt.Errorf("%w: the CLI engine needs %q and no %q", ErrInvalid, "command", "request")
	default:
		return fmt.Errorf("%w: the HTTP engine needs %q and no %q", ErrInvalid, "request", "command")
	}
}

func (sc scope) request(r Request) error {
	methods := []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete}
	if !slices.Contains(methods, r.Method) {
		return fmt.Errorf("%w: method %q is not one of %v", ErrInvalid, r.Method, methods)
	}

	if err := sc.urlPath(r.Path); err != nil {
		return err
	}

	for _, name := range sortedKeys(r.Query) {
		if err := sc.check(r.Query[name], slotPublic); err != nil {
			return fmt.Errorf("query %q: %w", name, err)
		}
	}

	if err := sc.pointerValues("body", r.Body, slotPrivate); err != nil {
		return err
	}

	if err := sc.results("response", r.Response); err != nil {
		return err
	}

	for _, status := range r.NotFound {
		if status < 400 || status > 599 {
			return fmt.Errorf("%w: notFound status %d is not 4xx or 5xx", ErrInvalid, status)
		}
	}

	if r.List != nil {
		return sc.list(*r.List)
	}

	return nil
}

// urlPath checks that the path starts with "/" and that each reference fills
// a whole segment, so that a value cannot add a segment or a query.
func (sc scope) urlPath(p string) error {
	if !strings.HasPrefix(p, "/") {
		return fmt.Errorf("%w: path %q must start with %q", ErrInvalid, p, "/")
	}

	for seg := range strings.SplitSeq(p[1:], "/") {
		if seg == "." || seg == ".." {
			return fmt.Errorf("%w: path %q has the segment %q", ErrInvalid, p, seg)
		}

		t, err := ParseTemplate(seg)
		if err != nil {
			return err
		}

		if _, whole := t.Whole(); len(t.Refs()) > 0 && !whole {
			return fmt.Errorf("%w: path segment %q must be all literal or one reference", ErrInvalid, seg)
		}

		if err := sc.check(seg, slotPublic); err != nil {
			return fmt.Errorf("path: %w", err)
		}
	}

	return nil
}

func (sc scope) list(l List) error {
	if !validPointer(l.Items) {
		return fmt.Errorf("%w: list items %q is not a JSON pointer", ErrInvalid, l.Items)
	}

	if len(l.Match) == 0 {
		return fmt.Errorf("%w: a list needs %q to select one item", ErrInvalid, "match")
	}

	if err := sc.pointerValues("match", l.Match, slotPrivate); err != nil {
		return err
	}

	if err := l.checkPages(); err != nil {
		return err
	}

	if l.PageSize < 0 || l.PageSize > 1000 || l.MaxPages < 0 || l.MaxPages > 1000 {
		return fmt.Errorf("%w: pageSize and maxPages must be from 0 to 1000", ErrInvalid)
	}

	return nil
}

func (l List) checkPages() error {
	switch l.Pages {
	case PageNone, PageLink:
		return nil
	case PageCursor:
		if l.CursorParam == "" || l.Cursor == "" || !validPointer(l.Cursor) {
			return fmt.Errorf("%w: cursor pages need %q and %q", ErrInvalid, "cursorParam", "cursor")
		}

		return nil
	case PageOffset:
		if l.OffsetParam == "" {
			return fmt.Errorf("%w: offset pages need %q", ErrInvalid, "offsetParam")
		}

		return nil
	default:
		return fmt.Errorf("%w: unknown pages %q", ErrInvalid, l.Pages)
	}
}

func (sc scope) command(c Command) error {
	for i, arg := range c.Args {
		if err := sc.check(arg, slotPublic); err != nil {
			return fmt.Errorf("args[%d]: %w", i, err)
		}

		if t, err := ParseTemplate(arg); err == nil {
			if ref, ok := t.OptionNameRef(); ok {
				return fmt.Errorf("%w: args[%d]: %s is in the name of an option, put it after %q",
					ErrInvalid, i, ref, "=")
			}
		}
	}

	if err := sc.pointerValues("stdin", c.Stdin, slotPrivate); err != nil {
		return err
	}

	if err := sc.results("output", c.Output); err != nil {
		return err
	}

	for _, code := range c.NotFound {
		if code < 1 || code > 255 {
			return fmt.Errorf("%w: notFound exit code %d is not from 1 to 255", ErrInvalid, code)
		}
	}

	return nil
}

// pointerValues checks a map from JSON pointers to templates.
func (sc scope) pointerValues(what string, m map[string]string, sl slot) error {
	for _, ptr := range sortedKeys(m) {
		if !validPointer(ptr) {
			return fmt.Errorf("%w: %s key %q is not a JSON pointer", ErrInvalid, what, ptr)
		}

		if err := sc.check(m[ptr], sl); err != nil {
			return fmt.Errorf("%s %q: %w", what, ptr, err)
		}
	}

	return nil
}

// results checks a map from attributes to JSON pointers in a reply or an
// output.
func (sc scope) results(what string, m map[string]string) error {
	for _, name := range sortedKeys(m) {
		a, ok := sc.attrs.Lookup(name)
		if !ok {
			return fmt.Errorf("%w: %s names no attribute %q", ErrInvalid, what, name)
		}

		if a.WriteOnly {
			return fmt.Errorf("%w: %s cannot fill the write-only attribute %q", ErrInvalid, what, name)
		}

		if !validPointer(m[name]) {
			return fmt.Errorf("%w: %s %q: %q is not a JSON pointer", ErrInvalid, what, name, m[name])
		}
	}

	return nil
}

// validPointer reports whether p is a JSON pointer: empty, or "/" and
// segments in which "~" is only "~0" or "~1".
func validPointer(p string) bool {
	if p == "" {
		return true
	}

	if !strings.HasPrefix(p, "/") {
		return false
	}

	rest := strings.ReplaceAll(strings.ReplaceAll(p, "~0", ""), "~1", "")

	return !strings.Contains(rest, "~")
}

func validateHTTPEngine(e HTTPEngine, config schema.Schema) error {
	sc := scope{
		attrs: schema.Schema{
			Version:    0,
			Attributes: nil,
		},
		config:      config,
		cli:         false,
		onlyID:      false,
		noWriteOnly: false,
	}

	if err := checkEndpointTemplate(sc, "http baseURL", e.BaseURL); err != nil {
		return err
	}

	if err := checkAuth(sc, e.Auth); err != nil {
		return fmt.Errorf("http auth: %w", err)
	}

	if e.Retry.MaxAttempts < 0 || e.Retry.MaxAttempts > 10 {
		return fmt.Errorf("%w: retry maxAttempts %d is not from 0 to 10", ErrInvalid, e.Retry.MaxAttempts)
	}

	for _, status := range e.Retry.Statuses {
		if status < 400 || status > 599 {
			return fmt.Errorf("%w: retry status %d is not 4xx or 5xx", ErrInvalid, status)
		}
	}

	if err := checkDuration("retry maxWait", e.Retry.MaxWait); err != nil {
		return err
	}

	return checkDuration("http timeout", e.Timeout)
}

// checkEndpointTemplate checks a URL of the HTTP engine. A URL with references is
// checked by CheckEndpoint when the provider is configured.
func checkEndpointTemplate(sc scope, what, raw string) error {
	if err := sc.check(raw, slotPublic); err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}

	t, err := ParseTemplate(raw)
	if err != nil || len(t.Refs()) > 0 {
		return err
	}

	if problem := endpointProblem(raw); problem != "" {
		return fmt.Errorf("%w: %s %q %s", ErrInvalid, what, raw, problem)
	}

	return nil
}

func checkAuth(sc scope, a Auth) error {
	fields, err := authFields(a)
	if err != nil {
		return err
	}

	for _, s := range fields.credentials {
		if err := sensitiveConfigRef(sc, s); err != nil {
			return err
		}
	}

	for _, p := range fields.public {
		if p == "" {
			return fmt.Errorf("%w: kind %q needs every field of its kind", ErrInvalid, a.Kind)
		}

		if err := sc.check(p, slotPublic); err != nil {
			return err
		}
	}

	if a.Kind == AuthOAuth2 {
		return checkEndpointTemplate(sc, "tokenURL", a.TokenURL)
	}

	return nil
}

// authNeeds lists the fields that a kind of authentication needs.
type authNeeds struct {
	// credentials must each be one reference to a sensitive configuration
	// attribute.
	credentials []string
	// public must each be set, and hold no sensitive reference.
	public []string
}

// authFields returns the fields that the kind of authentication needs.
func authFields(a Auth) (authNeeds, error) {
	switch a.Kind {
	case AuthNone:
		return authNeeds{credentials: nil, public: nil}, nil
	case AuthBearer:
		return authNeeds{credentials: []string{a.Token}, public: nil}, nil
	case AuthHeader:
		return authNeeds{credentials: []string{a.Token}, public: []string{a.Header}}, nil
	case AuthBasic:
		return authNeeds{credentials: []string{a.Password}, public: []string{a.Username}}, nil
	case AuthOAuth2:
		return authNeeds{credentials: []string{a.ClientSecret}, public: []string{a.ClientID, a.TokenURL}}, nil
	default:
		return authNeeds{credentials: nil, public: nil}, fmt.Errorf("%w: unknown kind %q", ErrInvalid, a.Kind)
	}
}

// sensitiveConfigRef checks that s is exactly one reference to a sensitive
// configuration attribute. A credential in the declaration itself would be
// in the provider binary and in version control.
func sensitiveConfigRef(sc scope, s string) error {
	t, err := ParseTemplate(s)
	if err != nil {
		return err
	}

	ref, whole := t.Whole()
	if !whole || ref.Kind != RefConfig {
		return fmt.Errorf("%w: a credential must be one reference such as {config.token}, not %q", ErrInvalid, s)
	}

	if a, ok := sc.config.Lookup(ref.Name); !ok || !a.Sensitive {
		return fmt.Errorf("%w: %s must name a sensitive configuration attribute", ErrInvalid, ref)
	}

	return nil
}

func validateCLIEngine(e CLIEngine, config schema.Schema) error {
	if !cliexec.ValidProgram(e.Program) {
		return fmt.Errorf("%w: cli program %q must be a bare name or a clean absolute path", ErrInvalid, e.Program)
	}

	sc := scope{
		attrs: schema.Schema{Version: 0, Attributes: nil}, config: config, cli: true, onlyID: false, noWriteOnly: false,
	}

	for _, name := range sortedKeys(e.Env) {
		if cliexec.ReservedEnv(name) {
			return fmt.Errorf("%w: cli env %q is not an allowed variable name", ErrInvalid, name)
		}

		if err := sc.check(e.Env[name], slotPublic); err != nil {
			return fmt.Errorf("cli env %q: %w", name, err)
		}
	}

	return checkDuration("cli timeout", e.Timeout)
}

func checkDuration(what, s string) error {
	if s == "" {
		return nil
	}

	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return fmt.Errorf("%w: %s %q is not a positive Go duration", ErrInvalid, what, s)
	}

	return nil
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}

	slices.Sort(keys)

	return keys
}
