package declaration

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"

	"opendustpan.org/providerkit/lifecycle"
	"opendustpan.org/providerkit/schema"
)

var (
	// ErrSchemaVersion reports a declaration without a "schema" key, or with
	// a version that this package does not read.
	ErrSchemaVersion = errors.New("unsupported declaration schema version")
	// ErrInvalid reports a declaration that breaks a rule of the format. The
	// wrapped message names the place in the file and the rule.
	ErrInvalid = errors.New("invalid declaration")
)

// maxDocumentBytes limits the size of a declaration. A provider with a few
// hundred types fits in far less.
const maxDocumentBytes = 4 << 20

var providerNamePattern = regexp.MustCompile(`^[a-z][a-z0-9]{0,31}$`)

// Parse reads and checks one declaration. Errors: ErrSchemaVersion for a
// missing or unknown version, ErrInvalid for a file that the version does
// not allow.
func Parse(r io.Reader) (Document, error) {
	var doc Document

	data, err := io.ReadAll(io.LimitReader(r, maxDocumentBytes+1))
	if err != nil {
		return doc, fmt.Errorf("read declaration: %w", err)
	}

	if len(data) > maxDocumentBytes {
		return doc, fmt.Errorf("%w: the file is larger than %d bytes", ErrInvalid, maxDocumentBytes)
	}

	var head struct {
		Schema *int `json:"schema"`
	}

	if err := json.Unmarshal(data, &head); err != nil {
		return doc, fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	switch {
	case head.Schema == nil:
		return doc, fmt.Errorf("%w: the key %q is missing", ErrSchemaVersion, "schema")
	case *head.Schema != SchemaVersion:
		return doc, fmt.Errorf("%w: %d, this build reads %d", ErrSchemaVersion, *head.Schema, SchemaVersion)
	default:
	}

	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()

	if err := dec.Decode(&doc); err != nil {
		return doc, fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	if dec.More() {
		return doc, fmt.Errorf("%w: the file holds more than one JSON value", ErrInvalid)
	}

	if err := doc.Validate(); err != nil {
		return doc, err
	}

	return doc, nil
}

// Validate checks every rule of the format that does not need the network or
// the OpenAPI document. It returns the first broken rule, wrapped in
// ErrInvalid.
func (d Document) Validate() error {
	if d.Schema != SchemaVersion {
		return fmt.Errorf("%w: %d", ErrSchemaVersion, d.Schema)
	}

	if !providerNamePattern.MatchString(d.Provider.Name) {
		return fmt.Errorf("%w: provider name %q must be lowercase letters and digits", ErrInvalid, d.Provider.Name)
	}

	config, err := d.ConfigSchema()
	if err != nil {
		return fmt.Errorf("provider config: %w", err)
	}

	for _, a := range config.Attributes {
		if a.Mode != schema.ModeRequired && a.Mode != schema.ModeOptional {
			return fmt.Errorf("%w: provider config attribute %q must be required or optional", ErrInvalid, a.Name)
		}
	}

	if _, err := lifecycle.WithFileForms(config); err != nil {
		return fmt.Errorf("%w: provider config: %w", ErrInvalid, err)
	}

	if err := d.validateEngine(config); err != nil {
		return err
	}

	return d.validateTypes(config)
}

func (d Document) validateEngine(config schema.Schema) error {
	switch {
	case d.HTTP != nil && d.CLI == nil:
		return validateHTTPEngine(*d.HTTP, config)
	case d.CLI != nil && d.HTTP == nil:
		return validateCLIEngine(*d.CLI, config)
	default:
		return fmt.Errorf("%w: set exactly one of %q and %q", ErrInvalid, "http", "cli")
	}
}

func (d Document) validateTypes(config schema.Schema) error {
	names := map[string]string{}

	for i, r := range d.Resources {
		where := fmt.Sprintf("resources[%d]", i)
		if err := uniqueName(names, r.Name, where); err != nil {
			return err
		}

		if err := d.validateResource(r, config); err != nil {
			return fmt.Errorf("%s (%s): %w", where, r.Name, err)
		}
	}

	names = map[string]string{}

	for i, ds := range d.DataSources {
		where := fmt.Sprintf("dataSources[%d]", i)
		if err := uniqueName(names, ds.Name, where); err != nil {
			return err
		}

		if err := d.validateDataSource(ds, config); err != nil {
			return fmt.Errorf("%s (%s): %w", where, ds.Name, err)
		}
	}

	return nil
}

func uniqueName(seen map[string]string, name, where string) error {
	if !schema.ValidName(name) {
		return fmt.Errorf("%w: %s: name %q is not snake_case", ErrInvalid, where, name)
	}

	if first, ok := seen[name]; ok {
		return fmt.Errorf("%w: %s: name %q is already used by %s", ErrInvalid, where, name, first)
	}

	seen[name] = where

	return nil
}

// ConfigSchema returns the schema of the provider configuration, as the
// declaration gives it. The built provider serves it with the file form of
// each secret, from lifecycle.WithFileForms.
func (d Document) ConfigSchema() (schema.Schema, error) {
	return toSchema(d.Provider.Config)
}

// Schema returns the schema of the resource type.
func (r Resource) Schema() (schema.Schema, error) {
	return toSchema(r.Attributes)
}

// Schema returns the schema of the data source type.
func (ds DataSource) Schema() (schema.Schema, error) {
	return toSchema(ds.Attributes)
}

func toSchema(attrs []Attribute) (schema.Schema, error) {
	s := schema.Schema{Version: 0, Attributes: make([]schema.Attribute, 0, len(attrs))}

	for _, a := range attrs {
		t, err := schema.ParseType(a.Type)
		if err != nil {
			return s, fmt.Errorf("%w: attribute %q: %w", ErrInvalid, a.Name, err)
		}

		mode := parseMode(a.Mode)
		if mode == schema.ModeInvalid {
			return s, fmt.Errorf("%w: attribute %q: unknown mode %q", ErrInvalid, a.Name, a.Mode)
		}

		s.Attributes = append(s.Attributes, schema.Attribute{
			Name: a.Name, Type: t, Mode: mode,
			Sensitive: a.Sensitive, WriteOnly: a.WriteOnly, Replace: a.Replace, Stable: a.Stable,
			Description: a.Description,
		})
	}

	if err := s.Validate(); err != nil {
		return s, fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	return s, nil
}

// parseMode returns the mode that the declaration spells, and ModeInvalid for
// an unknown one.
func parseMode(s string) schema.Mode {
	for _, m := range []schema.Mode{
		schema.ModeRequired, schema.ModeOptional, schema.ModeComputed, schema.ModeOptionalComputed,
	} {
		if m.String() == s {
			return m
		}
	}

	return schema.ModeInvalid
}
