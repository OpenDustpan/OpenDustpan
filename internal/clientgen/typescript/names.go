package typescript

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// tsString writes a string as a TypeScript string literal. JSON and
// TypeScript agree on this form, and the encoder escapes the quote, the
// backslash and every control character.
func tsString(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		// A Go string always encodes. The fallback keeps the output valid.
		return `""`
	}

	return string(b)
}

// identifier is the shape of a name that needs no quotes as a property key.
var identifier = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// propName writes a property key: the bare name when it is an identifier, and
// a string literal else.
func propName(name string) string {
	if identifier.MatchString(name) {
		return name
	}

	return tsString(name)
}

// jsdoc writes text as a JSDoc comment. A character that is not printable, for
// example a line break inside a line, becomes a space, and "*/" cannot end the
// comment. It returns the empty string for empty text.
func jsdoc(indent, text string) string {
	clean := strings.Map(func(r rune) rune {
		if unicode.IsPrint(r) {
			return r
		}

		return ' '
	}, strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n"))

	var lines []string

	for line := range strings.SplitSeq(clean, "\n") {
		line = strings.Join(strings.Fields(line), " ")
		if line != "" {
			lines = append(lines, strings.ReplaceAll(line, "*/", "*\\/"))
		}
	}

	switch len(lines) {
	case 0:
		return ""
	case 1:
		return indent + "/** " + lines[0] + " */\n"
	default:
		var b strings.Builder

		b.WriteString(indent + "/**\n")

		for _, line := range lines {
			b.WriteString(indent + " * " + line + "\n")
		}

		b.WriteString(indent + " */\n")

		return b.String()
	}
}

// exported makes a name start with a capital letter.
func exported(name string) string {
	r, n := utf8.DecodeRuneInString(name)

	return string(unicode.ToUpper(r)) + name[n:]
}

// globalTypes are the names of TypeScript and of the DOM library that the
// generated files use or that a schema must not hide, and the names that the
// generated files declare themselves.
func globalTypes() []string {
	return strings.Fields(`AbortSignal Array Boolean Date Error Exclude Function Headers JSON Map Number Object
		Partial Promise Readonly ReadonlyArray Record Request Response Set String Symbol URL URLSearchParams
		Client Metadata OperationId`)
}

// exportName finds the names that a runtime file exports.
var exportName = regexp.MustCompile(
	`(?m)^export (?:declare )?(?:abstract )?(?:async )?(?:class|interface|type|function|const|enum) ([A-Za-z_][A-Za-z0-9_]*)`,
)

// runtimeExports lists the names that the embedded runtime files export. A
// schema or an input type with one of these names has no valid name in the
// package, because the index file exports both.
func runtimeExports(sources map[string][]byte) []string {
	var names []string

	for _, src := range sources {
		for _, m := range exportName.FindAllSubmatch(src, -1) {
			names = append(names, string(m[1]))
		}
	}

	return names
}

// claim records that a name is taken, and returns an error when it was taken
// before.
func claim(seen map[string]string, name, by string, errKind error) error {
	if prev, ok := seen[name]; ok {
		return fmt.Errorf("%w: %s clashes with %s in the package", errKind, by, prev)
	}

	seen[name] = by

	return nil
}
