// Package setting models the settings resource of an installation and of a
// namespace.
//
// A setting is one declared key with a kind, a default and a rule for each
// layer that can hold a value. The effective value of a setting follows this
// precedence: a flag, an environment variable or the configuration file (the
// static layer), then the database layer, then the default. A setting that
// the static layer sets is locked: the API cannot change it. The API shows
// the effective value of each declared setting with its source, so a person
// can see why the server uses it.
//
// The resource of one scope has one revision. The revision changes at each
// change of a database value, and when the static layer changes an effective
// value or a source. A change names the revision that the caller read, and a
// stale revision changes nothing.
package setting

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"opendustpan.org/opendustpan/pkg/namespace"
)

// Level is the scope a declaration belongs to.
type Level string

// The levels of a declaration.
const (
	// LevelInstallation is a setting of the whole installation.
	LevelInstallation Level = "installation"
	// LevelNamespace is a setting of one namespace.
	LevelNamespace Level = "namespace"
)

// Kind is the value type of a setting. The text form of a value is the form
// that a store keeps.
type Kind string

// The kinds of a setting.
const (
	// KindString is a string.
	KindString Kind = "string"
	// KindBool is true or false.
	KindBool Kind = "bool"
	// KindInt is a whole number in decimal.
	KindInt Kind = "int"
)

// Apply says when a new effective value takes effect.
type Apply string

// The ways in which a new value takes effect.
const (
	// ApplyLive takes effect without a restart.
	ApplyLive Apply = "live"
	// ApplyRestart takes effect at the next start of the server.
	ApplyRestart Apply = "restart"
)

// Source is the layer that gives the effective value of a setting.
type Source string

// The sources of an effective value.
const (
	// SourceFlag is a command-line flag.
	SourceFlag Source = "flag"
	// SourceEnv is an environment variable.
	SourceEnv Source = "env"
	// SourceFile is the configuration file.
	SourceFile Source = "file"
	// SourceDatabase is a value that the API stored.
	SourceDatabase Source = "database"
	// SourceDefault is the default of the declaration.
	SourceDefault Source = "default"
)

// Limits of the resource.
const (
	// MaxValueBytes is the largest text of a string value.
	MaxValueBytes = 1024
	// MaxBodyBytes is the largest request body of a change.
	MaxBodyBytes = 16 * 1024
	// MaxKeyBytes is the largest key of a declaration.
	MaxKeyBytes = 64
)

// The errors of the package. Each wraps with the key or the reason.
var (
	// ErrInvalidDeclaration means that a declaration breaks a rule.
	ErrInvalidDeclaration = errors.New("setting: invalid declaration")
	// ErrUnknownKey means that no declaration of the scope has the key.
	ErrUnknownKey = errors.New("setting: unknown key")
	// ErrInvalidValue means that a value fails the kind or the check of its declaration.
	ErrInvalidValue = errors.New("setting: invalid value")
	// ErrInvalidPatch means that the body of a change is not a merge patch of settings.
	ErrInvalidPatch = errors.New("setting: invalid patch")
	// ErrLocked means that the static layer sets the key, so the API cannot set it.
	ErrLocked = errors.New("setting: locked by the static layer")
	// ErrStaticOnly means that only the static layer can set the key.
	ErrStaticOnly = errors.New("setting: only the static layer can set the key")
	// ErrPreconditionFailed means that the revision of the caller is not the stored revision.
	ErrPreconditionFailed = errors.New("setting: revision changed")
	// ErrFreshStepRequired means that the change reduces security and the caller has no fresh step.
	ErrFreshStepRequired = errors.New("setting: the change needs a fresh authentication")
	// ErrScope means that a scope or a level does not fit the call.
	ErrScope = errors.New("setting: invalid scope")
)

// KeyError is an error about one declared key. Key is always the key of a
// declaration, never text from a request. Reason is a fixed text that holds no
// value. For ErrLocked and ErrStaticOnly, Source and By say which layer sets
// the key.
type KeyError struct {
	Err    error
	Key    string
	Reason string
	Source Source
	By     string
}

// Error implements error.
func (e *KeyError) Error() string {
	if e.Reason == "" {
		return e.Err.Error() + ": " + e.Key
	}

	return e.Err.Error() + ": " + e.Key + ": " + e.Reason
}

// Unwrap returns the sentinel of the error.
func (e *KeyError) Unwrap() error { return e.Err }

// keyError returns a KeyError for key with the fixed text reason.
func keyError(err error, key, reason string) *KeyError {
	return &KeyError{Err: err, Key: key, Reason: reason, Source: "", By: ""}
}

// keyPattern is the grammar of a key: dot-separated words of letters, digits
// and hyphens, each word starting with a lower-case letter.
var keyPattern = regexp.MustCompile(`^[a-z][a-zA-Z0-9-]*(\.[a-z][a-zA-Z0-9-]*)*$`)

// Scope names the owner of a resource: the installation or one namespace.
type Scope struct {
	// Namespace is the namespace of the scope, empty for the installation.
	Namespace namespace.Name
}

// InstallationScope returns the scope of the installation.
func InstallationScope() Scope { return Scope{Namespace: ""} }

// NamespaceScope returns the scope of the namespace ns.
func NamespaceScope(ns namespace.Name) Scope { return Scope{Namespace: ns} }

// IsInstallation reports whether the scope is the installation.
func (s Scope) IsInstallation() bool { return s.Namespace == "" }

// Level returns the level of the declarations that apply to the scope.
func (s Scope) Level() Level {
	if s.IsInstallation() {
		return LevelInstallation
	}

	return LevelNamespace
}

// Declaration declares one setting.
type Declaration struct {
	// Key is the name of the setting on the wire and in the audit log.
	Key   string
	Level Level
	Kind  Kind
	// Default is the text form of the value when no layer sets one.
	Default string
	// Secret marks a setting whose value no answer, log line or audit entry
	// shows. The API shows only whether it is set.
	Secret bool
	// Database says whether the database layer can hold a value. A setting
	// that the database cannot hold is static only: a change through the API
	// answers a conflict.
	Database bool
	// StaticCredentialHost marks a setting that names a host to which the
	// server sends a static credential: a password, a key or a token that
	// the static layer holds. Such a setting is static only: the database
	// layer must not choose the host, because a stored value could point it
	// at another server while the credential stays.
	StaticCredentialHost bool
	Apply                Apply
	// Check refuses a value of the right kind, or is nil.
	Check func(value string) error
	// ReducesSecurity reports whether a change of the effective value from
	// old to next makes the installation or the namespace less secure, or is
	// nil when no change does. A change that reduces security needs a fresh
	// authentication.
	ReducesSecurity func(old, next string) bool
	// Doc is the one-line description that the reference shows.
	Doc string
}

// Validate checks the declaration.
func (d Declaration) Validate() error {
	if err := d.validateShape(); err != nil {
		return err
	}

	if d.Database {
		if err := d.CheckValue(d.Default); err != nil {
			return fmt.Errorf("%w: default of %s: %w", ErrInvalidDeclaration, d.Key, err)
		}
	}

	return nil
}

// CheckValue checks that value is a text form of the kind of d and passes
// its check.
func (d Declaration) CheckValue(value string) error {
	if len(value) > MaxValueBytes || !utf8.ValidString(value) || strings.ContainsAny(value, "\r\n\x00") {
		return keyError(ErrInvalidValue, d.Key, "the text is too long or holds a control character")
	}

	if err := d.checkKind(value); err != nil {
		return err
	}

	if d.Check != nil {
		if err := d.Check(value); err != nil {
			return keyError(ErrInvalidValue, d.Key, err.Error())
		}
	}

	return nil
}

// validateShape checks the parts of the declaration that need no value.
func (d Declaration) validateShape() error {
	if d.Key == "" || len(d.Key) > MaxKeyBytes || !keyPattern.MatchString(d.Key) {
		return fmt.Errorf("%w: key %q", ErrInvalidDeclaration, d.Key)
	}

	if d.Level != LevelInstallation && d.Level != LevelNamespace {
		return fmt.Errorf("%w: %s has no level", ErrInvalidDeclaration, d.Key)
	}

	if d.Apply != ApplyLive && d.Apply != ApplyRestart {
		return fmt.Errorf("%w: %s has no apply", ErrInvalidDeclaration, d.Key)
	}

	if d.Doc == "" {
		return fmt.Errorf("%w: %s has no description", ErrInvalidDeclaration, d.Key)
	}

	return d.validateLayers()
}

// validateLayers checks which layers the declaration allows.
func (d Declaration) validateLayers() error {
	if d.Secret && d.Database {
		// The database keeps no secret: a sealed value needs a data key that
		// no setting uses yet.
		return fmt.Errorf("%w: %s is a secret in the database layer", ErrInvalidDeclaration, d.Key)
	}

	if d.Database && d.StaticCredentialHost {
		// A host that receives a static credential must not have a database
		// layer: a holder of the manage-settings action could point it at
		// another server, and the server would send the credential there.
		return fmt.Errorf("%w: %s is a static-credential host in the database layer", ErrInvalidDeclaration, d.Key)
	}

	if d.Level == LevelNamespace && !d.Database {
		return fmt.Errorf("%w: %s is a namespace setting with no database layer", ErrInvalidDeclaration, d.Key)
	}

	return nil
}

// checkKind checks that value is a text form of the kind of d.
func (d Declaration) checkKind(value string) error {
	switch d.Kind {
	case KindString:
		return nil
	case KindBool:
		if value != "true" && value != "false" {
			return keyError(ErrInvalidValue, d.Key, "the value is not true or false")
		}

		return nil
	case KindInt:
		n, err := strconv.ParseInt(value, 10, 64)
		if err != nil || strconv.FormatInt(n, 10) != value {
			return keyError(ErrInvalidValue, d.Key, "the value is not a whole number")
		}

		return nil
	default:
		return keyError(ErrInvalidValue, d.Key, "the kind of the setting is not known")
	}
}

// StaticValue is what the static layer holds for a key.
type StaticValue struct {
	Source Source
	// Value is the text form of the value, empty for a secret.
	Value string
	// By names the flag, variable or file key that set the value.
	By string
}

// Static is the static layer of the installation. It never holds a value for
// a namespace setting.
type Static interface {
	// Lookup returns the value that a flag, an environment variable or the
	// configuration file gives for key. ok is false when none sets it.
	Lookup(key string) (value StaticValue, ok bool)
}

// NoStatic is a static layer that sets nothing.
type NoStatic struct{}

// Lookup implements Static.
func (NoStatic) Lookup(string) (StaticValue, bool) {
	return StaticValue{Source: "", Value: "", By: ""}, false
}

// Item is one setting in an answer: its effective value and where the value
// comes from.
type Item struct {
	Key   string
	Level Level
	Kind  Kind
	// Value is the effective value in text form. It is empty for a secret.
	Value   string
	Default string
	Source  Source
	// Locked is true when the API cannot set the key: a static layer sets it,
	// or only the static layer can.
	Locked bool
	// LockedBy says why the key is locked, empty when it is not.
	LockedBy string
	// DatabaseValue is the stored value, when one exists. Shadowed is true
	// when the static layer wins over it.
	DatabaseValue    string
	HasDatabaseValue bool
	Shadowed         bool
	Apply            Apply
	Secret           bool
	// Set says whether a secret has a value. For another setting it is true.
	Set bool
	// Database says whether the database layer can hold a value.
	Database bool
	// ReducesSecurity says whether a change of the setting can reduce
	// security, and then needs a fresh authentication.
	ReducesSecurity bool
	Doc             string
}

// Resource is the answer for one scope: every declared setting, in key
// order, and the revision.
type Resource struct {
	Items    []Item
	Revision int64
}
