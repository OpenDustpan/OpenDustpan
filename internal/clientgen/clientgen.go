// Package clientgen holds the ports of the client generator: the parser that
// reads an API description into the model, the template set of each language
// and the writer of the generated files.
//
// The package holds the parser (NewParser), the writer (NewWriter) and the
// rules for names and versions. The template set of a language is in a
// package of its own, for example golang. The package reads only the bytes of
// a published API description and imports no server package, so the same
// generator works for anyone who downloads the description.
//
// Rules for every implementation:
//
//   - The same description and the same options give the same bytes. An
//     implementation does not read the clock, the environment, the host
//     name or the order of a Go map.
//   - A construct outside the supported subset of OpenAPI is an error. The
//     parser never skips it and never guesses its meaning.
//   - A name or a text from the description goes into generated source only
//     through the encoder of the target language. A name that does not match
//     the identifier allowlist is an error.
package clientgen

import (
	"errors"

	"opendustpan.org/opendustpan/internal/clientgen/model"
)

// ErrUnsupported means that the description uses a construct outside the
// subset that the parser reads, for example an external $ref or a schema
// with allOf. The wrapped message names the JSON pointer of the construct.
var ErrUnsupported = errors.New("clientgen: construct is not supported")

// ErrInvalidDescription means that the description is not valid JSON, is not
// an OpenAPI 3.1 document, or breaks a rule of the subset, for example two
// operations with the same ID.
var ErrInvalidDescription = errors.New("clientgen: description is not valid")

// ErrInvalidName means that a name from the description, for example an
// operation ID, a schema name, a field name, a parameter name or an enum
// value, does not match the allowlist of names, or that it is a reserved word
// of a target language with no rule to change it.
var ErrInvalidName = errors.New("clientgen: name is not valid")

// ErrUnsafePath means that a generated file has a path that is absolute,
// holds "..", or is outside the output directory.
var ErrUnsafePath = errors.New("clientgen: file path is not safe")

// Parser reads an API description into the model.
//
// Parse returns ErrInvalidDescription, ErrUnsupported or ErrInvalidName,
// wrapped, and no partial model. The model it returns has every list in a
// fixed order: operations by ID, schemas by name, fields by wire name.
type Parser interface {
	Parse(doc []byte) (*model.API, error)
}

// Options are the values of one run of the generator that do not come from
// the description.
type Options struct {
	// Package is the package or module name of the client in the target
	// language, for example opendustpan.org/client for Go.
	Package string
	// ClientVersion is the version of the client release. Its major and minor
	// parts are those of the description version.
	ClientVersion string
	// ServerRelease is the release of the server that the description comes
	// from. The client carries it in its metadata and in its user agent,
	// next to the description version.
	ServerRelease string
	// GeneratorVersion is the version of the generator. It goes into the
	// header of each generated file.
	GeneratorVersion string
	// DescriptionSHA256 is the SHA-256 of the bytes of the description, in
	// lowercase hexadecimal. A client can carry it as a pin of the
	// description. A template set that has no use for it ignores it.
	DescriptionSHA256 string
}

// File is one generated file.
type File struct {
	// Path is relative to the output directory and uses forward slashes.
	Path string
	// Content is the full content of the file.
	Content []byte
}

// Language is the template set of one target language.
//
// Render returns the files in the order of their paths. It returns
// ErrInvalidName, wrapped, for a name that the language cannot hold, and
// ErrUnsafePath, wrapped, for a file path that is not safe. It never writes
// to the file system. The hand-written transport of the client is not part
// of its output.
type Language interface {
	// Name is the short name of the language, for example go or python.
	Name() string
	Render(api *model.API, opts Options) ([]File, error)
	// Header is the header line of a generated file, in the comment form of
	// the language. It names the generator version and the description
	// version, so that only clientgen output has it.
	Header(opts Options, descriptionVersion string) string
	// IsGenerated says whether the first bytes of a file hold a header that
	// Header could give, for any version. The header of another generator
	// does not match. The writer calls it to find the files that it may
	// replace or remove.
	IsGenerated(head []byte) bool
}

// Writer writes generated files under one output directory.
//
// Write returns ErrUnsafePath, wrapped, for a path outside the directory. It
// removes a file with the header of the language (Language.IsGenerated) that
// the new run does not give. It never changes a file that has no such header,
// also not a generated file of another generator.
type Writer interface {
	Write(dir string, files []File) error
}
