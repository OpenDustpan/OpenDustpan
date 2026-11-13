package cli

import (
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
)

// The text forms of a bool value.
const (
	valueTrue  = "true"
	valueFalse = "false"
)

// Kind is the value type of a setting: it selects the flag constructor and
// the environment parser of the declaration.
type Kind int

const (
	// KindString reads a string flag or variable.
	KindString Kind = iota
	// KindBool reads a bool flag or variable: true when the trimmed value is
	// true or 1, else false.
	KindBool
	// KindInt reads an int64 flag or variable in decimal. An invalid value
	// answers -1, so the caller rejects it with the flag names.
	KindInt
)

// Setting declares one setting once: the flag name (empty when the setting
// has no flag), the environment variable (empty when the setting has no
// variable), the default as a decimal or plain string, the --help text and
// the reference row. The same declaration registers the flag, prints --help
// and generates docs/reference/configuration.md, so the three never drift.
//
// File is the key of the setting in the configuration file (ADR 63), empty
// when the file cannot set it. Sensitive marks a setting that can hold a
// credential: a secret or the database URL. A log line shows a sensitive
// setting only as set or not set, never its value, a part of it or a hash of
// it (ADR 63 decision 6). URL marks a setting that holds a URL and is not
// sensitive: CheckURL refuses a value with user information, so its value
// can go in a log line.
type Setting struct {
	Flag      string
	Env       string
	Default   string
	Help      string
	Reference string
	Doc       string
	File      string
	Kind      Kind
	Sensitive bool
	URL       bool
	// StaticCredentialHost marks a setting that names a host to which the
	// server sends a static credential: a password, a key or a token that
	// the static layer holds. Such a setting is static only, and its
	// declaration refuses a database layer.
	StaticCredentialHost bool
	// StepInput marks a variable that the launcher sets for the process of a
	// step. The server does not read it, so it has no flag and no file key.
	StepInput bool
	// NoPasswordInFile refuses a URL with a password in the configuration
	// file and in the flag: a flag shows in a process list, so a secret
	// comes from a variable or a *_FILE key only.
	NoPasswordInFile bool
}

// The errors of a URL setting. Each error names the setting, never the
// value, because a refused value can hold a credential.
var (
	// ErrURLUserInfo is a URL setting whose value has user information.
	ErrURLUserInfo = errors.New("the URL has user information")
	// ErrURLInvalid is a URL setting whose value is not a URL.
	ErrURLInvalid = errors.New("the value is not a URL")
)

// ByFlag answers the declaration of the flag name. It panics when the flag
// has no declaration: every flag in the table must come from here, and the
// tests register every flag, so a typo fails fast.
func ByFlag(name string) *Setting {
	for _, s := range Settings {
		if s.Flag == name {
			return s
		}
	}

	panic(fmt.Sprintf("cli: flag --%s has no setting declaration", name))
}

// ByEnv answers the declaration of the environment variable, or nil when no
// declaration names it.
func ByEnv(name string) *Setting {
	for _, s := range Settings {
		if s.Env == name {
			return s
		}
	}

	return nil
}

// WithDoc sets the reference description of a setting with a flag and
// answers it, so the secrets section describes flagged settings without a
// second declaration.
func (s *Setting) WithDoc(doc string) *Setting {
	s.Doc = doc

	return s
}

// WithFile sets the configuration file key of a setting and answers it.
func (s *Setting) WithFile(key string) *Setting {
	s.File = key

	return s
}

// WithURL marks a setting as a URL that refuses user information and
// answers it.
func (s *Setting) WithURL() *Setting {
	s.URL = true

	return s
}

// CheckURL answers an error when s is a URL setting and value is not a URL
// or has user information (ADR 63 decision 6). The error names the setting,
// never the value. An empty value and a setting that is not a URL setting
// pass.
func (s *Setting) CheckURL(value string) error {
	if !s.URL || strings.TrimSpace(value) == "" {
		return nil
	}

	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil {
		return fmt.Errorf("%s: %w", s.Name(), ErrURLInvalid)
	}

	if parsed.User != nil {
		return fmt.Errorf("%s: %w. Remove the user name and the password from the URL", s.Name(), ErrURLUserInfo)
	}

	return nil
}

// WithNoPasswordInFile refuses a URL with a password for the setting in the
// configuration file and in the flag and answers it.
func (s *Setting) WithNoPasswordInFile() *Setting {
	s.NoPasswordInFile = true

	return s
}

// WithStepInput marks a variable that the launcher sets for the process of a
// step, and answers it. Such a variable holds no credential. A test checks this:
// the variable name and its description must not match the credential rules.
func (s *Setting) WithStepInput() *Setting {
	s.StepInput = true

	return s
}

// WithSensitive marks a setting as sensitive and answers it.
func (s *Setting) WithSensitive() *Setting {
	s.Sensitive = true

	return s
}

// WithStaticCredentialHost marks a setting as one that names a host to which
// the server sends a static credential, and answers it. Such a setting is
// static only: its declaration refuses a database layer.
func (s *Setting) WithStaticCredentialHost() *Setting {
	s.StaticCredentialHost = true

	return s
}

// Name answers the name that logs and errors use for the setting: the file
// key, else the flag name, else the environment variable.
func (s *Setting) Name() string {
	switch {
	case s.File != "":
		return s.File
	case s.Flag != "":
		return s.Flag
	default:
		return s.Env
	}
}

// ByFile answers the declaration of the configuration file key, or nil when
// no declaration names it.
func ByFile(key string) *Setting {
	for _, s := range Settings {
		if s.File != "" && s.File == key {
			return s
		}
	}

	return nil
}

// String registers the declaration as a string flag with its environment
// default and help.
func (s *Setting) String(fs *flag.FlagSet, target *string) {
	fs.StringVar(target, s.Flag, Env(s.Env, s.Default), s.Help)
}

// List registers the declaration as a repeatable string flag: each
// occurrence adds one item, and no item is split. The environment variable
// holds the items separated by white space, so an item can hold a comma.
// The first flag occurrence replaces the items of the variable.
func (s *Setting) List(fs *flag.FlagSet, target *[]string) {
	*target = strings.Fields(Env(s.Env, s.Default))
	fs.Var(&listValue{items: target, set: false}, s.Flag, s.Help)
}

// listValue is the flag.Value of a List flag.
type listValue struct {
	items *[]string
	set   bool
}

// String implements flag.Value.
func (v *listValue) String() string {
	if v == nil || v.items == nil {
		return ""
	}

	return strings.Join(*v.items, " ")
}

// Set implements flag.Value: the first call replaces the items of the
// environment, later calls add one item each.
func (v *listValue) Set(item string) error {
	if !v.set {
		*v.items = nil
		v.set = true
	}

	*v.items = append(*v.items, item)

	return nil
}

// setItems replaces the items with items. It is not an occurrence of the
// flag, so the first occurrence after it still replaces the items.
func (v *listValue) setItems(items []string) {
	if v.items != nil {
		*v.items = items
	}
}

// Bool registers the declaration as a bool flag with its environment
// default and help.
func (s *Setting) Bool(fs *flag.FlagSet, target *bool) {
	fs.BoolVar(target, s.Flag, EnvBool(s.Env), s.Help)
}

// Int64 registers the declaration as an int64 flag with its environment
// default and help. An invalid variable answers -1, so the caller rejects
// it with the flag names.
func (s *Setting) Int64(fs *flag.FlagSet, target *int64) {
	fs.Int64Var(target, s.Flag, EnvInt(s.Env, s.Default), s.Help)
}

// Env answers the string variable key, or fallback when it is unset.
func Env(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}

	return fallback
}

// EnvBool reads a boolean environment variable: true when the trimmed value
// is true or 1, else false.
func EnvBool(key string) bool {
	raw, ok := os.LookupEnv(key)
	if !ok {
		return false
	}

	return parseEnvBool(raw)
}

// parseEnvBool answers the value of a bool variable: true when the trimmed
// value is true or 1, else false.
func parseEnvBool(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case valueTrue, "1":
		return true
	default:
		return false
	}
}

// EnvInt answers the int64 variable key in decimal, or def when it is unset
// or blank. An invalid or negative value answers -1, so the caller rejects
// it with the flag names. Zero means the domain default, so only negative
// values fail.
func EnvInt(key, def string) int64 {
	raw, ok := os.LookupEnv(key)
	if !ok {
		return mustParseInt(def)
	}

	return parseEnvInt(raw, def)
}

// parseEnvInt answers the value of an int64 variable in decimal, def when it
// is blank, or -1 when it is invalid or negative.
func parseEnvInt(raw, def string) int64 {
	if strings.TrimSpace(raw) == "" {
		return mustParseInt(def)
	}

	parsed, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil || parsed < 0 {
		return -1
	}

	return parsed
}

// mustParseInt parses the fixed default of an int64 declaration. The
// defaults are constants in this file, so a bad one is a programmer error.
func mustParseInt(raw string) int64 {
	parsed, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil {
		panic(fmt.Sprintf("cli: bad int default %q: %v", raw, err))
	}

	return parsed
}

// DeclaredEnvs answers the sorted environment names of every declaration
// that names one.
func DeclaredEnvs() []string {
	var envs []string

	for _, s := range Settings {
		if s.Env != "" {
			envs = append(envs, s.Env)
		}
	}

	slices.Sort(envs)

	return envs
}

// SettingsFor answers the declarations of the flag names in order. It
// panics when a flag has no declaration, as ByFlag does.
func SettingsFor(flags ...string) []*Setting {
	out := make([]*Setting, 0, len(flags))

	for _, name := range flags {
		out = append(out, ByFlag(name))
	}

	return out
}
