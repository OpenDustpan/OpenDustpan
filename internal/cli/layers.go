package cli

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
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
type Resolved struct {
	Setting *Setting
	Source  Source
	Value   string
	Layers  []Layer
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
			redact(Resolved{Setting: setting, Source: winner.Source, Value: winner.Value, Layers: layers}),
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

// LogResolved writes the configuration log of ADR 63 decisions 7 and 8: one
// INFO line for each setting with its key, its effective value and its
// layer, and one WARN line for each setting that more than one layer sets,
// also when the values are equal. The default is not a layer for the WARN
// line. A sensitive setting shows only set or not set (decision 6): Resolve
// already removed its value.
func LogResolved(ctx context.Context, logger *slog.Logger, resolved []Resolved) {
	for _, r := range resolved {
		logger.LogAttrs(ctx, slog.LevelInfo, "setting",
			slog.String("key", r.Setting.Name()),
			slog.String("value", r.Value),
			slog.String("layer", string(r.Source)),
		)
	}

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
