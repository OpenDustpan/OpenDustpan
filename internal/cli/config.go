package cli

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

// MaxConfigBytes is the largest configuration file the server reads: 64 KiB
// (ADR 63 decision 3). The server checks the size before it parses the
// file, so a large file never reaches the parser.
const MaxConfigBytes = 64 << 10

// YAML tags that the decoder accepts. A value with any other tag, explicit
// or resolved, fails the type rule.
const (
	tagString = "!!str"
	tagBool   = "!!bool"
	tagInt    = "!!int"
	tagMap    = "!!map"
)

// The errors of the configuration file. Each error names the key and the
// rule, never the value, because a value can be a secret (ADR 63 decision 3).
var (
	// ErrConfigTooLarge is a file larger than MaxConfigBytes.
	ErrConfigTooLarge = errors.New("config file is larger than 64 KiB")
	// ErrConfigSyntax is a file that is not valid YAML.
	ErrConfigSyntax = errors.New("config file is not valid YAML")
	// ErrConfigShape is a file that is not one mapping of setting keys.
	ErrConfigShape = errors.New("config file must hold one mapping of setting keys")
	// ErrConfigAlias is a file with an anchor or an alias.
	ErrConfigAlias = errors.New("config file must not have anchors or aliases")
	// ErrConfigDuplicate is a key that the file sets more than once.
	ErrConfigDuplicate = errors.New("config file sets the key more than once")
	// ErrConfigUnknown is a key that no setting declares.
	ErrConfigUnknown = errors.New("config file key has no setting")
	// ErrConfigType is a value of the wrong type for its setting.
	ErrConfigType = errors.New("config file value has the wrong type")
	// ErrConfigPassword is a URL with a password for a setting that refuses
	// one in the file (ADR 63 decision 4).
	ErrConfigPassword = errors.New("config file value holds a password")

	errWantBool   = errors.New("want true or false")
	errWantInt    = errors.New("want a decimal integer of 0 or more")
	errWantString = errors.New("want a string")
)

// ConfigFile holds the values of a decoded configuration file: the file key
// of each setting that the file sets, mapped to its value in the text form
// of its flag. A bool is "true" or "false", an int is decimal.
type ConfigFile map[string]string

// ReadConfigFile reads and decodes the configuration file at path with the
// rules of ParseConfig. It refuses a file larger than MaxConfigBytes before
// it parses it.
func ReadConfigFile(path string) (ConfigFile, error) {
	file, err := os.Open(path) //nolint:gosec // The operator names the file with --config.
	if err != nil {
		return nil, fmt.Errorf("open config file: %w", err)
	}

	data, readErr := io.ReadAll(io.LimitReader(file, MaxConfigBytes+1))
	closeErr := file.Close()

	if readErr != nil {
		return nil, fmt.Errorf("read config file: %w", readErr)
	}

	if closeErr != nil {
		return nil, fmt.Errorf("close config file: %w", closeErr)
	}

	return ParseConfig(data)
}

// ParseConfig decodes a configuration file strictly (ADR 63 decision 3). It
// refuses a file that is larger than MaxConfigBytes, that is not valid YAML,
// that holds more than one document, that has an anchor or an alias, that
// sets a key twice, that sets a key no setting declares, or that gives a
// value of the wrong type. A bool is only true or false. A string setting
// takes only a string, so `no` stays the string "no". An int setting takes
// only a decimal integer of 0 or more. An empty file sets nothing.
//
// The decoder walks a yaml.Node tree and never decodes into a Go value, so
// no YAML 1.1 rule (yes, on, 0x10) applies and no alias expands.
func ParseConfig(data []byte) (ConfigFile, error) {
	if len(data) > MaxConfigBytes {
		return nil, ErrConfigTooLarge
	}

	dec := yaml.NewDecoder(bytes.NewReader(data))

	var doc yaml.Node

	if err := dec.Decode(&doc); err != nil {
		if errors.Is(err, io.EOF) {
			return ConfigFile{}, nil
		}

		return nil, syntaxError(err)
	}

	var extra yaml.Node

	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%w: the file holds more than one document", ErrConfigShape)
	}

	if err := refuseAliases(&doc); err != nil {
		return nil, err
	}

	if doc.Kind != yaml.DocumentNode || len(doc.Content) != 1 {
		return nil, ErrConfigShape
	}

	root := doc.Content[0]
	if root.Kind != yaml.MappingNode || root.ShortTag() != tagMap {
		return nil, ErrConfigShape
	}

	return decodeMapping(root)
}

// maxKeyInError is the most characters of a key that an error shows, so an
// error never echoes a long text of the file.
const maxKeyInError = 32

// yamlLine finds the line number in an error of the YAML library.
var yamlLine = regexp.MustCompile(`line (\d+)`)

// syntaxError answers ErrConfigSyntax with the line number of err when it
// has one. It never holds the message of the library: a message such as
// "unknown anchor" can echo text of the file.
func syntaxError(err error) error {
	if match := yamlLine.FindStringSubmatch(err.Error()); match != nil {
		return fmt.Errorf("%w: line %s", ErrConfigSyntax, match[1])
	}

	return ErrConfigSyntax
}

// errorKey answers key for an error: quoted, and cut to maxKeyInError
// characters.
func errorKey(key string) string {
	runes := []rune(key)
	if len(runes) > maxKeyInError {
		return strconv.Quote(string(runes[:maxKeyInError])) + " (cut)"
	}

	return strconv.Quote(key)
}

// refuseAliases answers ErrConfigAlias when a node of the tree has an anchor
// or is an alias. The parser keeps an alias as one node, so the walk never
// expands it.
func refuseAliases(node *yaml.Node) error {
	if node.Anchor != "" || node.Kind == yaml.AliasNode {
		return fmt.Errorf("%w: line %d", ErrConfigAlias, node.Line)
	}

	for _, child := range node.Content {
		if err := refuseAliases(child); err != nil {
			return err
		}
	}

	return nil
}

// decodeMapping decodes the root mapping: each key once, each key declared,
// each value of the type of its setting.
func decodeMapping(root *yaml.Node) (ConfigFile, error) {
	out := ConfigFile{}

	for i := 0; i+1 < len(root.Content); i += 2 {
		keyNode, valueNode := root.Content[i], root.Content[i+1]

		if keyNode.Kind != yaml.ScalarNode || keyNode.ShortTag() != tagString {
			return nil, fmt.Errorf("%w: line %d: a key must be a string", ErrConfigShape, keyNode.Line)
		}

		key := keyNode.Value

		if _, seen := out[key]; seen {
			return nil, fmt.Errorf("%w: %s at line %d", ErrConfigDuplicate, errorKey(key), keyNode.Line)
		}

		setting := ByFile(key)
		if setting == nil {
			return nil, fmt.Errorf("%w: %s at line %d", ErrConfigUnknown, errorKey(key), keyNode.Line)
		}

		value, err := decodeValue(setting, valueNode)
		if err != nil {
			return nil, fmt.Errorf("%w: %s at line %d: %w", ErrConfigType, errorKey(key), valueNode.Line, err)
		}

		if setting.NoPasswordInFile && hasPassword(value) {
			return nil, fmt.Errorf(
				"%w: %s at line %d: set a database URL with a password in %s, never in the file",
				ErrConfigPassword, errorKey(key), valueNode.Line, setting.Env,
			)
		}

		out[key] = value
	}

	return out, nil
}

// hasPassword reports whether value is a URL with a password. A value that
// does not parse as a URL counts as one: the parser fails on many passwords
// that are not escaped, and the error must not let such a value pass.
func hasPassword(value string) bool {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil {
		return true
	}

	_, set := parsed.User.Password()

	return set
}

// decodeValue answers the value of node in the text form of the flag of
// setting, or an error that states the rule. The error never holds the
// value.
func decodeValue(setting *Setting, node *yaml.Node) (string, error) {
	if setting.Kind == KindBool {
		return decodeBool(node)
	}

	if setting.Kind == KindInt {
		return decodeInt(node)
	}

	return decodeString(node)
}

// decodeBool answers a bool value: only the plain scalars true and false.
func decodeBool(node *yaml.Node) (string, error) {
	if node.Kind != yaml.ScalarNode || node.ShortTag() != tagBool {
		return "", errWantBool
	}

	if node.Value != valueTrue && node.Value != valueFalse {
		return "", errWantBool
	}

	return node.Value, nil
}

// decimalForm is the only text of an int value: 0, or a digit 1 to 9 and
// more digits. No sign, no leading zero, no underscore.
var decimalForm = regexp.MustCompile(`^(0|[1-9]\d*)$`)

// decodeInt answers an int value: only a decimal integer of 0 or more that
// fits in an int64, with no sign and no leading zero.
func decodeInt(node *yaml.Node) (string, error) {
	if node.Kind != yaml.ScalarNode || node.ShortTag() != tagInt {
		return "", errWantInt
	}

	if !decimalForm.MatchString(node.Value) {
		return "", errWantInt
	}

	parsed, err := strconv.ParseInt(node.Value, 10, 64)
	if err != nil || parsed < 0 {
		return "", errWantInt
	}

	return strconv.FormatInt(parsed, 10), nil
}

// decodeString answers a string value: only a scalar that YAML resolves to
// a string, so no, on and yes stay strings and 12 and true are refused.
func decodeString(node *yaml.Node) (string, error) {
	if node.Kind != yaml.ScalarNode || node.ShortTag() != tagString {
		return "", errWantString
	}

	return node.Value, nil
}
