package selfservice

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"opendustpan.org/opendustpan/internal/policy"
	"opendustpan.org/opendustpan/internal/quota"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// BlueprintSchemaVersion is the newest schema version of a blueprint
// document that this server reads.
const BlueprintSchemaVersion = 1

// Limits of a blueprint document.
const (
	MaxBlueprintBytes = 256 << 10
	MaxInputs         = 64
	MaxRules          = 32
	MaxPolicyRules    = 64
	MaxExprBytes      = 4 << 10
	MaxMessageBytes   = 1 << 10
	MaxStaticValues   = 1000
	MaxFilters        = 8
	MaxChoiceDepth    = 8
)

// Blueprint is the document of a blueprint: a program that a platform team
// offers for self-service, with its inputs and its rules. A draft and each
// published version hold one.
type Blueprint struct {
	Program     Program  `json:"program"`
	Platform    Platform `json:"platform"`
	Delivery    Delivery `json:"delivery"`
	Name        string   `json:"name"`
	Title       string   `json:"title"`
	Description string   `json:"description,omitempty"`
	// Tool is the tool name of the units of the blueprint, as the unit
	// reference writes it, for example "opentofu" or "pulumi".
	Tool string `json:"tool"`
	// ExprVersion is the Expr version that compiled the expressions. A
	// publish sets it. A draft leaves it empty.
	ExprVersion ExprVersionName `json:"exprVersion,omitempty"`
	// UnitName is an Expr expression that gives the tool's own name of the
	// unit. The server computes it at the first submit and keeps it.
	UnitName string `json:"unitName"`
	// SourcePrincipal is the ID of the service principal that reads the
	// sources of the choices. The server reads the outputs of each source
	// with the grants of this principal, and checks its unit:read on the
	// source at each computation of the options. It is required when an
	// input has a source, and empty when none has one.
	SourcePrincipal string       `json:"sourcePrincipal,omitempty"`
	Inputs          []Input      `json:"inputs"`
	Policy          []PolicyRule `json:"policy,omitempty"`
	Quotas          []QuotaRule  `json:"quotas,omitempty"`
	SchemaVersion   int          `json:"schemaVersion"`
}

// ExprVersionName is the version of the Expr engine that compiled a blueprint.
// It has its own type so that the description of the API can say that the
// server sets it.
type ExprVersionName string

// Program is the module or the component that a blueprint adds to. Exactly
// one of the two is set.
type Program struct {
	Module    *Module    `json:"module,omitempty"`
	Component *Component `json:"component,omitempty"`
}

// Module is an OpenTofu module: a source address and an exact version, or a
// directory of a repository at a tag or a commit.
type Module struct {
	Source     string `json:"source,omitempty"`
	Version    string `json:"version,omitempty"`
	Connection string `json:"connection,omitempty"`
	Repository string `json:"repository,omitempty"`
	Directory  string `json:"directory,omitempty"`
	Revision   string `json:"revision,omitempty"`
}

// Component is a Pulumi component: its package, an exact version and its
// type token.
type Component struct {
	Package string `json:"package"`
	Version string `json:"version"`
	Type    string `json:"type"`
}

// InputType is the type of an input.
type InputType string

// The types of an input.
const (
	TypeString     InputType = "string"
	TypeInteger    InputType = "integer"
	TypeNumber     InputType = "number"
	TypeBoolean    InputType = "boolean"
	TypeStringList InputType = "string-list"
)

// Input is one value that a requester can set. Maps names the variable of
// the module or the config key of the component that the answer sets
// (ValidKey). An input with a default is never required. No input is
// secret: a secret comes from the profile of the blueprint.
type Input struct {
	Choice      *Choice         `json:"choice,omitempty"`
	Name        string          `json:"name"`
	Title       string          `json:"title,omitempty"`
	Description string          `json:"description,omitempty"`
	Type        InputType       `json:"type"`
	Maps        string          `json:"maps"`
	Default     json.RawMessage `json:"default,omitempty"`
	Validate    []Rule          `json:"validate,omitempty"`
	Required    bool            `json:"required,omitempty"`
}

// Rule is one validation rule of an input: an Expr expression that gives a
// boolean, and the message of a failure.
type Rule struct {
	Rule    string `json:"rule"`
	Message string `json:"message"`
}

// Choice gives the options of an input: a fixed list of values, or a source
// of unit outputs. Exactly one of the two is set.
type Choice struct {
	Source *Source  `json:"source,omitempty"`
	Values []string `json:"values,omitempty"`
}

// PolicyRule is one policy rule of a blueprint, for the built-in Expr
// engine. When is an Expr condition. Approvals and Groups are for
// policy.EffectApprove.
type PolicyRule struct {
	When      string        `json:"when"`
	Effect    policy.Effect `json:"effect"`
	Message   string        `json:"message,omitempty"`
	Groups    []string      `json:"groups,omitempty"`
	Approvals int           `json:"approvals,omitempty"`
}

// QuotaRule is one quota rule of a blueprint, as the document writes it.
// Window is a duration in the form of time.ParseDuration, for example
// "24h". Rule and Message are for quota.KindExpr.
type QuotaRule struct {
	Kind    quota.Kind  `json:"kind"`
	Scope   quota.Scope `json:"scope,omitempty"`
	Window  string      `json:"window,omitempty"`
	Rule    string      `json:"rule,omitempty"`
	Message string      `json:"message,omitempty"`
	Max     int         `json:"max,omitempty"`
}

// QuotaRule returns r as a rule of package quota. It returns ErrInvalid for
// a window that does not parse, and the error of quota.Rule.Validate.
func (r QuotaRule) QuotaRule() (quota.Rule, error) {
	var window time.Duration

	if r.Window != "" {
		d, err := time.ParseDuration(r.Window)
		if err != nil {
			return quota.Rule{}, fmt.Errorf("%w: window %q", ErrInvalid, r.Window)
		}

		window = d
	}

	out := quota.Rule{Kind: r.Kind, Scope: r.Scope, Expr: r.Rule, Message: r.Message, Window: window, Max: r.Max}
	if err := out.Validate(); err != nil {
		return quota.Rule{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	return out, nil
}

// Platform holds what the platform team sets and the requester cannot see
// or change: fixed values of variables or config keys, the profile that
// supplies the secrets of a run, and the runner pool.
type Platform struct {
	Values  map[string]json.RawMessage `json:"values,omitempty"`
	Profile string                     `json:"profile,omitempty"`
	Pool    string                     `json:"pool,omitempty"`
}

// Delivery lists the homes that the requests of a blueprint can use, and
// the limits of a home in git.
type Delivery struct {
	Git     *GitDelivery `json:"git,omitempty"`
	Default HomeKind     `json:"default"`
	Homes   []HomeKind   `json:"homes"`
}

// GitDelivery limits the requests in git: the deliveries that they can use,
// and the repositories that they can name. Empty Repositories allows each
// repository of the forge connections of the namespace.
type GitDelivery struct {
	Deliveries   []job.Delivery `json:"deliveries,omitempty"`
	Repositories []string       `json:"repositories,omitempty"`
}

// Version is a published version of a blueprint. It never changes, except
// Retired, and ExprVersion after a compile with a new Expr version.
type Version struct {
	PublishedAt time.Time
	Blueprint   Blueprint
	Namespace   namespace.Name
	PublishedBy string
	Number      int
	// DraftVersion is the version of the draft that this version published.
	DraftVersion int64
	Retired      bool
}

// Ref returns the reference of the blueprint of v.
func (v *Version) Ref() BlueprintRef {
	return BlueprintRef{Namespace: v.Namespace, Name: v.Blueprint.Name}
}

// ParseBlueprint reads a blueprint document. It refuses a document over
// MaxBlueprintBytes with ErrLimit, a duplicate key, an unknown field and
// data after the document with ErrInvalid, and a schema version above
// BlueprintSchemaVersion with ErrSchemaVersion. It then applies
// Blueprint.Validate.
func ParseBlueprint(data []byte) (Blueprint, error) {
	var b Blueprint

	if err := decodeStrict(data, MaxBlueprintBytes, "blueprint", &b); err != nil {
		return Blueprint{}, err
	}

	if err := b.Validate(); err != nil {
		return Blueprint{}, err
	}

	return b, nil
}

// Validate checks each rule of a blueprint that needs no engine: the
// header, the program, each input, the keys, the one-home rule, the order
// of the choices, the source principal, the policy and quota rules, and the
// delivery. A publish also compiles each expression with the Evaluator.
func (b *Blueprint) Validate() error {
	checks := []func() error{
		b.validateHeader, b.validateProgram, b.validateInputs, b.validateKeys,
		func() error { return CheckOneHome(b) },
		func() error { _, err := Order(b.Inputs); return err },
		b.validateSourcePrincipal, b.validatePolicy, b.validateQuotas, b.validateDelivery,
	}

	for _, check := range checks {
		if err := check(); err != nil {
			return err
		}
	}

	return nil
}

// InputNamed returns the input name of b, and false when b has none.
func (b *Blueprint) InputNamed(name string) (*Input, bool) {
	for i := range b.Inputs {
		if b.Inputs[i].Name == name {
			return &b.Inputs[i], true
		}
	}

	return nil, false
}

// The tools of a blueprint, as the unit reference writes them.
const (
	ToolOpenTofu = "opentofu"
	ToolPulumi   = "pulumi"
)

// MaxKeyBytes bounds a key of Input.Maps and of Platform.Values.
const MaxKeyBytes = 128

// reservedModuleArgs are the names that a module block of OpenTofu keeps
// for itself. No variable of a module can have them.
func reservedModuleArgs() []string {
	return []string{"source", "version", "providers", "count", "for_each", "lifecycle", "depends_on", "locals"}
}

// ValidKey reports whether key is a key that a blueprint of tool can set:
//   - for OpenTofu, a variable of the module: an HCL identifier, which is a
//     letter or '_' and then letters, digits, '_' and '-', and not a name
//     that a module block keeps, such as source or count,
//   - for Pulumi, a config key: a name, or a namespace, a ':' and a name,
//     where each part is a letter and then letters, digits, '_' and '-'.
//
// A key is at most MaxKeyBytes. The render writes an answer only as data,
// in the variables file or in the stack settings, and writes only keys of
// this form in the program text, so an answer can never change the program.
func ValidKey(tool, key string) bool {
	if key == "" || len(key) > MaxKeyBytes {
		return false
	}

	switch tool {
	case ToolOpenTofu:
		return hclIdentifier(key) && !slices.Contains(reservedModuleArgs(), key)
	case ToolPulumi:
		ns, name, found := strings.Cut(key, ":")
		if !found {
			return configName(key)
		}

		return configName(ns) && configName(name)
	default:
		return false
	}
}

func hclIdentifier(s string) bool {
	for i := range len(s) {
		c := s[i]

		switch {
		case isLetter(c) || c == '_':
		case i > 0 && ((c >= '0' && c <= '9') || c == '-'):
		default:
			return false
		}
	}

	return true
}

func configName(s string) bool {
	if s == "" || !isLetter(s[0]) {
		return false
	}

	for i := range len(s) {
		c := s[i]
		if !isLetter(c) && (c < '0' || c > '9') && c != '_' && c != '-' {
			return false
		}
	}

	return true
}

// NormalizeKey returns the form of key that the one-home check compares:
// in lowercase, and, for Pulumi, with no namespace when the namespace is
// the project of the render, which is the name of the blueprint. So
// "Size", "size" and "postgres:size" are one key of the blueprint postgres.
// Two keys that differ only in case are one key, also for a tool that
// tells them apart, so that a person who reads them never sees two values
// for what looks like one.
func NormalizeKey(b *Blueprint, key string) string {
	if b.Tool == ToolPulumi {
		key = strings.TrimPrefix(key, b.Name+":")
	}

	return strings.ToLower(key)
}

// HasSource reports whether an input of b has a choice with a source.
func (b *Blueprint) HasSource() bool {
	return slices.ContainsFunc(b.Inputs, func(in Input) bool { return in.Choice != nil && in.Choice.Source != nil })
}

// CheckOneHome returns ErrOneHome when a key has more than one home in b:
// two inputs that map to one key, or an input that maps to a key that a
// platform value also sets, compared in the form of NormalizeKey. The error
// names the key. The render checks the keys against the other homes of the
// unit in the same form.
func CheckOneHome(b *Blueprint) error {
	owner := make(map[string]string, len(b.Inputs)+len(b.Platform.Values))

	keys := make([]string, 0, len(b.Platform.Values))
	for key := range b.Platform.Values {
		keys = append(keys, key)
	}

	slices.Sort(keys)

	for _, key := range keys {
		n := NormalizeKey(b, key)
		if have, ok := owner[n]; ok {
			return fmt.Errorf("%w: key %q is set by the platform values and by %s", ErrOneHome, key, have)
		}

		owner[n] = fmt.Sprintf("the platform value %q", key)
	}

	for i := range b.Inputs {
		in := &b.Inputs[i]

		n := NormalizeKey(b, in.Maps)
		if have, ok := owner[n]; ok {
			return fmt.Errorf("%w: key %q is set by the input %s and by %s", ErrOneHome, in.Maps, in.Name, have)
		}

		owner[n] = "the input " + in.Name
	}

	return nil
}

func (b *Blueprint) validateKeys() error {
	for i := range b.Inputs {
		if in := &b.Inputs[i]; !ValidKey(b.Tool, in.Maps) {
			return fmt.Errorf("%w: input %s maps to %q, which is not a key of %s", ErrInvalid, in.Name, in.Maps, b.Tool)
		}
	}

	for key := range b.Platform.Values {
		if !ValidKey(b.Tool, key) {
			return fmt.Errorf("%w: the platform value %q is not a key of %s", ErrInvalid, key, b.Tool)
		}
	}

	return nil
}

func (b *Blueprint) validateProgram() error {
	if err := b.Program.validate(); err != nil {
		return err
	}

	switch {
	case b.Tool == ToolOpenTofu && b.Program.Module == nil:
		return fmt.Errorf("%w: a blueprint of OpenTofu names a module", ErrInvalid)
	case b.Tool == ToolPulumi && b.Program.Component == nil:
		return fmt.Errorf("%w: a blueprint of Pulumi names a component", ErrInvalid)
	default:
		return nil
	}
}

func (b *Blueprint) validateSourcePrincipal() error {
	switch {
	case b.HasSource() && b.SourcePrincipal == "":
		return fmt.Errorf("%w: a blueprint with a source names the principal that reads it", ErrInvalid)
	case !b.HasSource() && b.SourcePrincipal != "":
		return fmt.Errorf("%w: a blueprint with no source names no source principal", ErrInvalid)
	case b.SourcePrincipal == "":
		return nil
	default:
	}

	if _, err := identity.ParsePrincipalObject(b.SourcePrincipal); err != nil {
		return fmt.Errorf("%w: source principal %q", ErrInvalid, b.SourcePrincipal)
	}

	return nil
}

func (b *Blueprint) validateHeader() error {
	switch {
	case b.SchemaVersion < 1:
		return fmt.Errorf("%w: schemaVersion is missing", ErrInvalid)
	case b.SchemaVersion > BlueprintSchemaVersion:
		return fmt.Errorf("%w: blueprint version %d, this server reads up to version %d",
			ErrSchemaVersion, b.SchemaVersion, BlueprintSchemaVersion)
	case !ValidName(b.Name):
		return fmt.Errorf("%w: blueprint name %q", ErrInvalid, b.Name)
	case b.Title == "":
		return fmt.Errorf("%w: a blueprint needs a title", ErrInvalid)
	case b.Tool != ToolOpenTofu && b.Tool != ToolPulumi:
		return fmt.Errorf("%w: tool %q, give %s or %s", ErrInvalid, b.Tool, ToolOpenTofu, ToolPulumi)
	default:
		return checkExpr("unitName", b.UnitName)
	}
}

func (b *Blueprint) validateInputs() error {
	if len(b.Inputs) > MaxInputs {
		return fmt.Errorf("%w: %d inputs, the limit is %d", ErrLimit, len(b.Inputs), MaxInputs)
	}

	seen := make(map[string]bool, len(b.Inputs))

	for i := range b.Inputs {
		in := &b.Inputs[i]
		if seen[in.Name] {
			return fmt.Errorf("%w: input %q appears twice", ErrInvalid, in.Name)
		}

		seen[in.Name] = true

		if err := in.validate(); err != nil {
			return fmt.Errorf("input %s: %w", in.Name, err)
		}
	}

	return nil
}

func (b *Blueprint) validatePolicy() error {
	if len(b.Policy) > MaxPolicyRules {
		return fmt.Errorf("%w: %d policy rules, the limit is %d", ErrLimit, len(b.Policy), MaxPolicyRules)
	}

	for i := range b.Policy {
		if err := b.Policy[i].validate(); err != nil {
			return fmt.Errorf("policy rule %d: %w", i, err)
		}
	}

	return nil
}

func (b *Blueprint) validateQuotas() error {
	if len(b.Quotas) > quota.MaxRules {
		return fmt.Errorf("%w: %d quota rules, the limit is %d", ErrLimit, len(b.Quotas), quota.MaxRules)
	}

	for i, r := range b.Quotas {
		if _, err := r.QuotaRule(); err != nil {
			return fmt.Errorf("quota rule %d: %w", i, err)
		}
	}

	return nil
}

func (b *Blueprint) validateDelivery() error {
	d := &b.Delivery
	if len(d.Homes) == 0 {
		return fmt.Errorf("%w: delivery names no home", ErrInvalid)
	}

	for _, h := range d.Homes {
		if !h.Valid() {
			return fmt.Errorf("%w: home %q", ErrInvalid, h)
		}
	}

	if !slices.Contains(d.Homes, d.Default) {
		return fmt.Errorf("%w: the default home %q is not in the homes", ErrInvalid, d.Default)
	}

	if d.Git == nil {
		return nil
	}

	for _, del := range d.Git.Deliveries {
		if del != job.DeliveryReviewRequest && del != job.DeliveryDirectPush {
			return fmt.Errorf("%w: delivery %q", ErrInvalid, del)
		}
	}

	for _, r := range d.Git.Repositories {
		if err := checkRepository("the git delivery", r); err != nil {
			return err
		}
	}

	return nil
}

func (p *Program) validate() error {
	switch {
	case (p.Module == nil) == (p.Component == nil):
		return fmt.Errorf("%w: a program names exactly one module or one component", ErrInvalid)
	case p.Component != nil:
		return p.Component.validate()
	default:
		return p.Module.validate()
	}
}

func (c *Component) validate() error {
	if c.Package == "" || c.Type == "" || !exactVersion(c.Version) {
		return fmt.Errorf("%w: a component needs a package, a type and an exact version", ErrInvalid)
	}

	return nil
}

func (m *Module) validate() error {
	if m.Source != "" {
		if !exactVersion(m.Version) || m.Repository != "" || m.Revision != "" {
			return fmt.Errorf("%w: a module from a registry needs an exact version and no repository", ErrInvalid)
		}

		return nil
	}

	if m.Repository == "" || m.Revision == "" || m.Version != "" {
		return fmt.Errorf("%w: a module from git needs a repository and a tag or a commit", ErrInvalid)
	}

	return checkRepository("a module from git", m.Repository)
}

// exactVersion reports whether v names one version, not a range.
func exactVersion(v string) bool {
	return v != "" && !strings.ContainsAny(v, " ~^<>=*,|xX")
}

func (in *Input) validate() error {
	switch {
	case !ValidInputName(in.Name):
		return fmt.Errorf("%w: input name %q", ErrInvalid, in.Name)
	case !in.Type.valid():
		return fmt.Errorf("%w: type %q", ErrInvalid, in.Type)
	case in.Maps == "":
		return fmt.Errorf("%w: maps names no variable or config key", ErrInvalid)
	case in.Required && len(in.Default) > 0:
		return fmt.Errorf("%w: an input with a default is not required", ErrInvalid)
	case len(in.Default) > 0 && !in.Type.accepts(in.Default):
		return fmt.Errorf("%w: the default is not of the type %s", ErrInvalid, in.Type)
	default:
	}

	if err := validateRules(in.Validate); err != nil {
		return err
	}

	if in.Choice != nil {
		return in.validateChoice()
	}

	return nil
}

func validateRules(rules []Rule) error {
	if len(rules) > MaxRules {
		return fmt.Errorf("%w: %d rules, the limit is %d", ErrLimit, len(rules), MaxRules)
	}

	for _, r := range rules {
		if err := checkExpr("rule", r.Rule); err != nil {
			return err
		}

		if r.Message == "" || len(r.Message) > MaxMessageBytes {
			return fmt.Errorf("%w: a rule needs a message of 1 to %d bytes", ErrInvalid, MaxMessageBytes)
		}
	}

	return nil
}

func (in *Input) validateChoice() error {
	c := in.Choice

	switch {
	case in.Type != TypeString && in.Type != TypeStringList:
		return fmt.Errorf("%w: a choice is a string or a string-list", ErrInvalid)
	case (c.Source == nil) == (len(c.Values) == 0):
		return fmt.Errorf("%w: a choice has values or a source", ErrInvalid)
	case len(c.Values) > MaxStaticValues:
		return fmt.Errorf("%w: %d values, the limit is %d", ErrLimit, len(c.Values), MaxStaticValues)
	case c.Source != nil:
		return c.Source.validate(in.Name)
	default:
	}

	seen := make(map[string]bool, len(c.Values))
	for _, v := range c.Values {
		if seen[v] {
			return fmt.Errorf("%w: value %q appears twice", ErrInvalid, v)
		}

		seen[v] = true
	}

	return nil
}

func (r *PolicyRule) validate() error {
	if err := checkExpr("when", r.When); err != nil {
		return err
	}

	switch r.Effect {
	case policy.EffectDeny:
		if r.Approvals != 0 || len(r.Groups) != 0 {
			return fmt.Errorf("%w: deny has no approvals", ErrInvalid)
		}
	case policy.EffectApprove:
		if r.Approvals < 1 || r.Approvals > policy.MaxApprovals {
			return fmt.Errorf("%w: give 1 to %d approvals", ErrInvalid, policy.MaxApprovals)
		}
	case policy.EffectAllow:
		return fmt.Errorf("%w: a rule gives deny or approve, and no rule that matches allows", ErrInvalid)
	default:
		return fmt.Errorf("%w: effect %q, give deny or approve", ErrInvalid, r.Effect)
	}

	return nil
}

func (t InputType) valid() bool {
	switch t {
	case TypeString, TypeInteger, TypeNumber, TypeBoolean, TypeStringList:
		return true
	default:
		return false
	}
}

// accepts reports whether raw is a value of type t.
func (t InputType) accepts(raw json.RawMessage) bool {
	shape, err := answerShape(raw)
	if err != nil {
		return false
	}

	switch t {
	case TypeString:
		return shape == ShapeString
	case TypeBoolean:
		return shape == ShapeBoolean
	case TypeNumber:
		return shape == ShapeNumber
	case TypeInteger:
		var n float64

		return shape == ShapeNumber && json.Unmarshal(raw, &n) == nil && n == float64(int64(n))
	case TypeStringList:
		return shape == ShapeList
	default:
		return false
	}
}

func checkExpr(field, source string) error {
	if source == "" || len(source) > MaxExprBytes {
		return fmt.Errorf("%w: %s needs an expression of 1 to %d bytes", ErrInvalid, field, MaxExprBytes)
	}

	return nil
}
