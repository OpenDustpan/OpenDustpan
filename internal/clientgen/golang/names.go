package golang

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"opendustpan.org/opendustpan/internal/clientgen"
)

// goKeywords returns the reserved words of Go. A name that is one of them gets
// one underscore at its end.
func goKeywords() []string {
	return []string{
		"break", "case", "chan", "const", "continue", "default", "defer", "else", "fallthrough", "for", "func",
		"go", "goto", "if", "import", "interface", "map", "package", "range", "return", "select", "struct",
		"switch", "type", "var",
	}
}

// localNames returns the names that the generated code uses for its own
// variables. A parameter with one of these names gets one underscore at its end.
func localNames() []string {
	return []string{
		"ctx", "c", "opts", "body", "query", "req", "out", "resp", "err", "values", "p", "client", "url", "fmt",
	}
}

// universeNames returns the names of the universe scope of Go. A path
// parameter with one of them would hide the name in the generated method, for
// example nil, true or string.
func universeNames() []string {
	return []string{
		"any", "bool", "byte", "comparable", "complex64", "complex128", "error", "float32", "float64", "int",
		"int8", "int16", "int32", "int64", "rune", "string", "uint", "uint8", "uint16", "uint32", "uint64",
		"uintptr", "true", "false", "iota", "nil", "append", "cap", "clear", "close", "complex", "copy", "delete",
		"imag", "len", "make", "max", "min", "new", "panic", "print", "println", "real", "recover",
	}
}

// importNames returns the names of the packages that the generated files and
// the adapter import. A path parameter with one of them would hide the
// package.
func importNames() []string {
	return []string{"context", "http", "json", "fmt", "url", "strings", "client"}
}

// packageNames returns the lower-case package-level names of the hand-written
// transport and of the adapter. A path parameter with one of them would hide
// it in the generated method or in the table of the adapter. A test reads both
// hand-written files and fails when this list is not complete.
func packageNames() []string {
	return []string{
		"maxResponseBytes", "maxErrorTextBytes", "ifMatchMode", "ifMatchNone", "ifMatchOptional",
		"ifMatchRequired", "request", "callConfig", "newKey", "isJSON", "errorOf",
		"callTimeout", "protocolVersion", "clientModule", "maxLineBytes", "codeParse", "codeMethodNotFound",
		"codeInvalidParams", "codeServer", "message", "rpcError", "reply", "success", "initializeParams",
		"callParams", "callResult", "errorResult", "main", "run", "fail", "handle", "call", "errUnknownOperation",
		"finish", "errorOutcome", "callOperation",
	}
}

// reservedTypes returns the exported package-level names that the
// hand-written transport and the metadata file declare. A schema, and the
// query type of an operation, with one of these names has no valid name in Go.
// A test reads the transport and fails when this list is not complete.
func reservedTypes() []string {
	return []string{
		"Client", "Response", "Error", "Option", "CallOption", "New", "WithToken", "WithHTTPClient",
		"WithIdempotencyKey", "WithIfMatch", "ErrInvalidResponse", "ErrOption", "ErrBody",
		"Version", "DescriptionVersion", "ServerRelease", "GeneratorVersion",
	}
}

// goString writes a string as a Go string literal.
func goString(s string) string { return strconv.Quote(s) }

// comment writes text as lines of a Go comment. A character that is not
// printable, for example a line break, becomes a space, so text from the
// description cannot end the comment or start a line of code.
func comment(indent, text string) string {
	clean := strings.Map(func(r rune) rune {
		if unicode.IsPrint(r) {
			return r
		}

		return ' '
	}, strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n"))

	var b strings.Builder

	for line := range strings.SplitSeq(clean, "\n") {
		line = strings.Join(strings.Fields(line), " ")
		if line == "" {
			continue
		}

		b.WriteString(indent + "// " + line + "\n")
	}

	return b.String()
}

// exported makes the name of a field or a type start with a capital letter.
func exported(name string) string {
	r, n := utf8.DecodeRuneInString(name)

	return string(unicode.ToUpper(r)) + name[n:]
}

// words splits a wire name or a path parameter on its separators.
func words(name string) []string {
	return strings.FieldsFunc(name, func(r rune) bool { return strings.ContainsRune("._:-", r) })
}

// camel turns a snake_case path parameter into lowerCamelCase: sink_id gives
// sinkId. The reserved words of Go, the names of the universe scope and of the
// imports, and the names of the generated code and of its hand-written files
// get one underscore at the end.
func camel(name string) string {
	parts := strings.Split(name, "_")

	var b strings.Builder

	for i, p := range parts {
		switch {
		case p == "":
		case i == 0:
			b.WriteString(p)
		default:
			b.WriteString(exported(p))
		}
	}

	out := b.String()
	if out == "" {
		out = "_"
	}

	for _, reserved := range [][]string{
		goKeywords(), localNames(), universeNames(), importNames(), packageNames(),
	} {
		if slices.Contains(reserved, out) {
			out += "_"

			break
		}
	}

	return out
}

// pascal turns an enum value into the tail of the name of its constant:
// awaiting-approval gives AwaitingApproval.
func pascal(value string) string {
	var b strings.Builder

	for _, w := range words(value) {
		b.WriteString(exported(w))
	}

	return b.String()
}

// safeToken checks a version or a release before it goes into source.
func safeToken(what, v string) error {
	if v == "" || len(v) > 64 ||
		strings.Trim(v, "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ.+-_") != "" {
		return fmt.Errorf("%w: %s %q", clientgen.ErrInvalidName, what, v)
	}

	return nil
}
