package cli

import (
	"flag"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
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
type Setting struct {
	Flag      string
	Env       string
	Default   string
	Help      string
	Reference string
	Doc       string
	Kind      Kind
}

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

// String registers the declaration as a string flag with its environment
// default and help.
func (s *Setting) String(fs *flag.FlagSet, target *string) {
	fs.StringVar(target, s.Flag, Env(s.Env, s.Default), s.Help)
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

	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "true", "1":
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
	if !ok || strings.TrimSpace(raw) == "" {
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
