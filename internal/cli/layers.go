package cli

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"

	"opendustpan.org/opendustpan/pkg/audit"
)

// Source is the layer that gives a setting its value (ADR 63 decision 7).
type Source string

// The layers of the static configuration, in precedence order: a flag, then
// an environment variable, then the configuration file, then the built-in
// default (ADR 63 decision 1). The database layer comes in a later change.
const (
	SourceFlag    Source = "flag"
	SourceEnv     Source = "env"
	SourceFile    Source = "file"
	SourceDefault Source = "default"
)

// Log values of a sensitive setting: the log shows only whether it holds a
// value (ADR 63 decision 6).
const (
	valueSet    = "set"
	valueNotSet = "not set"
)

// warnFixedAttrs is the number of attributes of a WARN line before the one
// attribute for each layer: the key, the layer that wins and the layers
// that lost.
const warnFixedAttrs = 3

// LookupFunc answers the value of an environment variable, as os.LookupEnv.
type LookupFunc func(key string) (string, bool)

// Resolved is the effective value of one setting after the precedence, with
// the layers that hold a value, highest first. For a sensitive setting,
// Value is only set or not set, and the value of each layer is only set:
// no text of a sensitive value leaves Resolve (ADR 63 decision 6).
//
// The fingerprint is an HMAC of the effective value under a random key of
// this process. It lets Changes see that a sensitive value changed without
// keeping the value. It never leaves the process: no log line and no audit
// entry holds it, and %v and %+v print no part of it.
type Resolved struct {
	Setting     *Setting
	Source      Source
	Value       string
	Layers      []Layer
	fingerprint *fingerprintValue
}

// fingerprintValue is an HMAC of a setting value under fingerprintKey. Its
// String and Format methods print nothing, and Resolved holds it behind a
// pointer, so fmt prints at most an address and never its bytes.
type fingerprintValue [sha256.Size]byte

// String answers "": a fingerprint never shows in text.
func (*fingerprintValue) String() string { return "" }

// Format prints nothing for every verb: a fingerprint never shows in text.
func (*fingerprintValue) Format(fmt.State, rune) {}

// sameFingerprint reports whether a and b hold the same fingerprint.
func sameFingerprint(a, b *fingerprintValue) bool {
	if a == nil || b == nil {
		return a == b
	}

	return *a == *b
}

// Layer is one layer that holds a value for a setting.
type Layer struct {
	Source Source
	Value  string
}

// Resolve applies the precedence flag > env > file > default to each
// setting (ADR 63 decision 1) and answers the result in the order of
// settings. It sets each flag in fs to its effective value, so the
// variables that fs binds hold the result. fs must be parsed. lookup reads
// the environment and file holds the decoded configuration file, nil when
// there is none.
//
// A variable that is empty or holds only whitespace is not set, for every
// type, so the next layer wins. The value of any other variable keeps the
// rules of Env, EnvBool and EnvInt: a bool is true for true or 1, and a bad
// int is -1, so the caller refuses it with the flag name.
func Resolve(fs *flag.FlagSet, settings []*Setting, lookup LookupFunc, file ConfigFile) ([]Resolved, error) {
	visited := map[string]bool{}

	fs.Visit(func(f *flag.Flag) { visited[f.Name] = true })

	out := make([]Resolved, 0, len(settings))

	for _, setting := range settings {
		layers := collectLayers(fs, setting, visited, lookup, file)

		winner := Layer{Source: SourceDefault, Value: setting.Default}
		if len(layers) > 0 {
			winner = layers[0]
		}

		if err := setting.CheckURL(winner.Value); err != nil {
			return nil, err
		}

		if err := applyWinner(fs, setting, winner); err != nil {
			return nil, err
		}

		out = append(
			out,
			redact(Resolved{
				Setting:     setting,
				Source:      winner.Source,
				Value:       winner.Value,
				Layers:      layers,
				fingerprint: fingerprint(winner.Value),
			}),
		)
	}

	return out, nil
}

// applyWinner sets the flag of setting in fs to the value of winner, when
// the flag exists and the command line does not set it. A List flag gets
// the items of the value separated by white space, as its variable holds
// them, so a variable with two items does not become one item.
func applyWinner(fs *flag.FlagSet, setting *Setting, winner Layer) error {
	if setting.Flag == "" || winner.Source == SourceFlag {
		return nil
	}

	f := fs.Lookup(setting.Flag)
	if f == nil {
		return nil
	}

	if list, ok := f.Value.(*listValue); ok {
		list.setItems(strings.Fields(winner.Value))

		return nil
	}

	if err := fs.Set(setting.Flag, winner.Value); err != nil {
		return fmt.Errorf("setting %s from %s: %w", setting.Name(), winner.Source, err)
	}

	return nil
}

// collectLayers answers the layers that hold a value for setting, highest
// first: the flag when the command line sets it, the variable when the
// environment has it, and the file key when the file has it.
func collectLayers(
	fs *flag.FlagSet, setting *Setting, visited map[string]bool, lookup LookupFunc, file ConfigFile,
) []Layer {
	var layers []Layer

	if f := fs.Lookup(setting.Flag); setting.Flag != "" && visited[setting.Flag] && f != nil {
		layers = append(layers, Layer{Source: SourceFlag, Value: f.Value.String()})
	}

	if raw, ok := lookupSet(lookup, setting.Env); setting.Env != "" && ok {
		layers = append(layers, Layer{Source: SourceEnv, Value: envValue(setting, raw)})
	}

	if value, ok := file[setting.File]; setting.File != "" && ok {
		layers = append(layers, Layer{Source: SourceFile, Value: value})
	}

	return layers
}

// fingerprintKey is the random HMAC key of this process. A fingerprint
// never leaves the process, and a new process makes a new key, so a
// fingerprint cannot be compared with a guess outside the process.
//
//nolint:gochecknoglobals // One key for the life of the process: each Resolve must use the same key.
var fingerprintKey = func() []byte {
	key := make([]byte, sha256.Size)
	_, _ = rand.Read(key) // crypto/rand.Read never fails: it panics.

	return key
}()

// fingerprintToken starts a value that SnapshotEnv put in place of a
// sensitive variable: the token holds the fingerprint of the value in hex,
// never the value. The NUL character cannot start a value from the
// environment, so no real value reads as a token.
const fingerprintToken = "\x00fingerprint:"

// fingerprint answers the HMAC of value under fingerprintKey. For a token of
// SnapshotEnv it answers the fingerprint that the token holds, so a reload
// against the snapshot sees the same fingerprint as the start against the
// real variable.
func fingerprint(value string) *fingerprintValue {
	var out fingerprintValue

	if hexText, ok := strings.CutPrefix(value, fingerprintToken); ok {
		if raw, err := hex.DecodeString(hexText); err == nil && len(raw) == len(out) {
			copy(out[:], raw)

			return &out
		}
	}

	mac := hmac.New(sha256.New, fingerprintKey)
	mac.Write([]byte(value))
	copy(out[:], mac.Sum(nil))

	return &out
}

// TakesEffectNextStart is when a changed setting applies. No setting is
// live yet (RFC 68 keeps that open), so each change applies at the next
// start.
const TakesEffectNextStart = "next start"

// Changes answers the settings whose effective value differs between old and
// current, sorted by key, as audit changes with the old and the new value,
// the new layer and TakesEffectNextStart (ADR 63 decision 9). A sensitive setting shows only set
// or not set, also when its value changed from one secret to another. A
// setting that only one of the two lists holds is not a change.
func Changes(old, current []Resolved) []audit.Change {
	before := make(map[string]Resolved, len(old))
	for _, r := range old {
		before[r.Setting.Name()] = r
	}

	var out []audit.Change

	for _, r := range current {
		prev, ok := before[r.Setting.Name()]
		if !ok || sameFingerprint(prev.fingerprint, r.fingerprint) {
			continue
		}

		oldValue := prev.Value
		out = append(out, audit.Change{
			Key: r.Setting.Name(), Old: &oldValue, New: r.Value, Layer: string(r.Source),
			TakesEffect: TakesEffectNextStart,
		})
	}

	slices.SortFunc(out, func(a, b audit.Change) int { return strings.Compare(a.Key, b.Key) })

	return out
}

// LogChanges writes one INFO line for each changed setting with its key, its
// old value, its new value and its new layer (ADR 63 decision 9). The
// values of a sensitive setting are only set or not set.
func LogChanges(ctx context.Context, logger *slog.Logger, changes []audit.Change) {
	for _, change := range changes {
		old := ""
		if change.Old != nil {
			old = *change.Old
		}

		logger.LogAttrs(ctx, slog.LevelInfo, "setting changed",
			slog.String("key", change.Key),
			slog.String("old", old),
			slog.String("value", change.New),
			slog.String("layer", change.Layer),
			slog.String("takes_effect", change.TakesEffect),
		)
	}
}

// redact answers r with the values of a sensitive setting replaced: the
// effective value by set or not set, the value of each layer by set.
func redact(r Resolved) Resolved {
	if !r.Setting.Sensitive {
		return r
	}

	if r.Value == "" {
		r.Value = valueNotSet
	} else {
		r.Value = valueSet
	}

	for i := range r.Layers {
		r.Layers[i].Value = valueSet
	}

	return r
}

// envValue answers the raw value of a variable in the text form of the flag
// of setting, with the rules of Env, EnvBool and EnvInt.
func envValue(setting *Setting, raw string) string {
	if setting.Kind == KindBool {
		return strconv.FormatBool(parseEnvBool(raw))
	}

	if setting.Kind == KindInt {
		return strconv.FormatInt(parseEnvInt(raw, setting.Default), 10)
	}

	return raw
}

// lookupSet answers the value of the variable key, and false when it is
// unset, empty or holds only whitespace: such a variable is not a layer, so
// the next layer wins (the lead's review of ADR 63 part 1).
func lookupSet(lookup LookupFunc, key string) (string, bool) {
	value, ok := lookup(key)
	if !ok || strings.TrimSpace(value) == "" {
		return "", false
	}

	return value, true
}

// LayeredLookup answers a lookup that reads the environment first and then
// the configuration file. A variable that is empty or holds only whitespace
// is not set, so the file wins, for the variables that the server reads by name
// (for example DUSTPAN_ADMIN_TOKEN_FILE). A variable whose declaration has
// no file key reads only the environment.
func LayeredLookup(lookup LookupFunc, file ConfigFile) LookupFunc {
	return func(key string) (string, bool) {
		if value, ok := lookupSet(lookup, key); ok {
			return value, true
		}

		setting := ByEnv(key)
		if setting == nil || setting.File == "" {
			return "", false
		}

		value, ok := file[setting.File]

		return value, ok
	}
}

// LogResolved writes the configuration log of ADR 63 decisions 7 and 8:
// LogSettings, then LogOverrides.
func LogResolved(ctx context.Context, logger *slog.Logger, resolved []Resolved) {
	LogSettings(ctx, logger, resolved)
	LogOverrides(ctx, logger, resolved)
}

// LogSettings writes one INFO line for each setting with its key, its
// effective value and its layer (ADR 63 decision 7). A sensitive setting
// shows only set or not set (decision 6): Resolve already removed its value.
func LogSettings(ctx context.Context, logger *slog.Logger, resolved []Resolved) {
	for _, r := range resolved {
		logger.LogAttrs(ctx, slog.LevelInfo, "setting",
			slog.String("key", r.Setting.Name()),
			slog.String("value", r.Value),
			slog.String("layer", string(r.Source)),
		)
	}
}

// LogOverrides writes one WARN line for each setting that more than one
// layer sets, also when the values are equal (ADR 63 decision 8). The
// default is not a layer for this rule.
func LogOverrides(ctx context.Context, logger *slog.Logger, resolved []Resolved) {
	for _, r := range resolved {
		if len(r.Layers) <= 1 {
			continue
		}

		overridden := make([]string, 0, len(r.Layers)-1)

		for _, layer := range r.Layers[1:] {
			overridden = append(overridden, string(layer.Source))
		}

		attrs := make([]slog.Attr, 0, len(r.Layers)+warnFixedAttrs)
		attrs = append(attrs,
			slog.String("key", r.Setting.Name()),
			slog.String("layer", string(r.Source)),
			slog.String("overridden", strings.Join(overridden, ",")),
		)

		for _, layer := range r.Layers {
			attrs = append(attrs, slog.String(string(layer.Source), layer.Value))
		}

		logger.LogAttrs(ctx, slog.LevelWarn, "setting has a value in more than one layer", attrs...)
	}
}

// SnapshotEnv answers a lookup over the values that lookup gives for keys
// now, for the reloads of the configuration file: a variable changes only at
// a restart (ADR 63 decision 5), and the server clears the secret variables
// after it reads them. The snapshot keeps the value of a setting that is not
// sensitive. For a sensitive setting it keeps only a token with the
// fingerprint of the value, so the process never holds a secret for its
// whole life. The start resolves against the real variables; a reload
// resolves against the snapshot and sees the same fingerprints.
func SnapshotEnv(lookup LookupFunc, keys []string) LookupFunc {
	values := make(map[string]string, len(keys))

	for _, key := range keys {
		value, ok := lookup(key)
		if !ok {
			continue
		}

		if setting := ByEnv(key); setting != nil && setting.Sensitive && strings.TrimSpace(value) != "" {
			value = fingerprintToken + hex.EncodeToString(fingerprint(value)[:])
		}

		values[key] = value
	}

	return func(key string) (string, bool) {
		value, ok := values[key]

		return value, ok
	}
}
