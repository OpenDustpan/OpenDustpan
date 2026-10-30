// Package catalogstore holds the rules of the catalog blueprint tables that the
// SQLite and PostgreSQL adapters share: the JSON form of a blueprint, the
// checks of a write, and the check of a stored row. A row is untrusted when it
// is read, because a backup, a restore or another version of the server can
// have written it. Both adapters call the same functions, so they apply one
// rule.
package catalogstore

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"opendustpan.org/opendustpan/internal/catalog"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
)

// ErrCorrupt reports a stored row that does not pass the checks of a blueprint.
// The adapters do not serve such a row. The error also wraps catalog.ErrInvalid.
var ErrCorrupt = errors.New("stored blueprint is not valid")

// VersionRow is one row of the table of published versions. Namespace is the
// name of the namespace of the row. Retired is 1 or 0.
type VersionRow struct {
	Namespace     string
	Name          string
	DocumentJSON  string
	ExprVersion   string
	PublishedBy   string
	PublishedAtMs int64
	Number        int64
	DraftVersion  int64
	Retired       int64
}

// EncodeDraft returns the JSON of the draft b. It returns catalog.ErrLimit for a
// document above catalog.MaxBlueprintBytes.
func EncodeDraft(b *catalog.Blueprint) (string, error) {
	return encode(b)
}

// EncodeVersion returns the JSON of b for a published version. It leaves out the
// Expr version, which the row holds in its own column.
func EncodeVersion(b *catalog.Blueprint) (string, error) {
	doc := *b
	doc.ExprVersion = ""

	return encode(&doc)
}

func encode(b *catalog.Blueprint) (string, error) {
	data, err := json.Marshal(b)
	if err != nil {
		return "", fmt.Errorf("%w: encode blueprint: %w", catalog.ErrInvalid, err)
	}

	if len(data) > catalog.MaxBlueprintBytes {
		return "", fmt.Errorf("%w: the blueprint has %d bytes, the limit is %d",
			catalog.ErrLimit, len(data), catalog.MaxBlueprintBytes)
	}

	return string(data), nil
}

// CheckDraftWrite checks a write of the draft b at version under fence. The
// service checks the same facts, and this check stays so that a new caller
// cannot write a draft that the rules refuse.
//
// Defense in depth: the service and the API handler check the name and the
// entries too. This check does not rely on them. It also keeps a draft from a
// namespace other than the one of the fence, whatever the entries say.
func CheckDraftWrite(b *catalog.Blueprint, version int64, fence namespace.Fence, e []audit.Entry) error {
	if !catalog.ValidName(b.Name) {
		return fmt.Errorf("%w: blueprint name %q", catalog.ErrInvalid, b.Name)
	}

	if version < 0 {
		return fmt.Errorf("%w: draft version %d", catalog.ErrInvalid, version)
	}

	return checkEntries(fence, e)
}

// CheckPublication checks p under fence. It applies Blueprint.Validate to the
// document, because a published version never changes.
//
// Defense in depth: the service validates and compiles the document before
// it calls the store. This check stays so that a new caller cannot publish
// a document that the rules refuse.
func CheckPublication(p *catalog.Publication, fence namespace.Fence, e []audit.Entry) error {
	switch {
	case p.Ref.Namespace != fence.Namespace:
		return fmt.Errorf("%w: blueprint %s, fence names %q", namespace.ErrFenced, p.Ref, fence.Namespace)
	case p.Ref.Name != p.Blueprint.Name:
		return fmt.Errorf("%w: reference %s, blueprint %q", catalog.ErrInvalid, p.Ref, p.Blueprint.Name)
	case p.DraftVersion < 1:
		return fmt.Errorf("%w: draft %s at %d", catalog.ErrConflict, p.Ref, p.DraftVersion)
	}

	if err := p.Blueprint.Validate(); err != nil {
		return fmt.Errorf("publish %s: %w", p.Ref, err)
	}

	if p.Blueprint.ExprVersion != "" {
		if err := CheckExprVersion(p.Blueprint.ExprVersion); err != nil {
			return err
		}
	}

	return checkEntries(fence, e)
}

// CheckRef checks that ref is in the namespace of fence, for a write of a
// version. Defense in depth: the entries and the queries name the namespace
// too. This check stays so that a ref of another namespace never writes.
func CheckRef(ref catalog.BlueprintRef, n int, fence namespace.Fence, e []audit.Entry) error {
	if ref.Namespace != fence.Namespace {
		return fmt.Errorf("%w: blueprint %s, fence names %q", namespace.ErrFenced, ref, fence.Namespace)
	}

	if n < 1 {
		return fmt.Errorf("%w: %s version %d", catalog.ErrNotFound, ref, n)
	}

	return checkEntries(fence, e)
}

// checkEntries returns audit.ErrInvalidEntry for an entry that fails its
// checks or names another namespace than the fence.
func checkEntries(fence namespace.Fence, e []audit.Entry) error {
	for i := range e {
		if err := e[i].Validate(); err != nil {
			return fmt.Errorf("audit entry: %w", err)
		}

		if e[i].Namespace != fence.Namespace {
			return fmt.Errorf("%w: entry names %q, fence names %q",
				audit.ErrInvalidEntry, e[i].Namespace, fence.Namespace)
		}
	}

	return nil
}

// Draft reads the JSON that EncodeDraft wrote for the draft ref. It refuses a
// field that it does not know, text after the document, and a document that
// names another blueprint. A draft can be incomplete, so it applies no
// Blueprint.Validate.
func Draft(ref catalog.BlueprintRef, doc string) (catalog.Blueprint, error) {
	if len(doc) > catalog.MaxBlueprintBytes {
		return catalog.Blueprint{}, fmt.Errorf(
			"%w: draft %s has %d bytes: %w",
			ErrCorrupt,
			ref,
			len(doc),
			catalog.ErrLimit,
		)
	}

	dec := json.NewDecoder(bytes.NewReader([]byte(doc)))
	dec.DisallowUnknownFields()

	var b catalog.Blueprint
	if err := dec.Decode(&b); err != nil {
		return catalog.Blueprint{}, fmt.Errorf("%w: draft %s: %w", ErrCorrupt, ref, err)
	}

	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return catalog.Blueprint{}, fmt.Errorf("%w: draft %s: text after the document", ErrCorrupt, ref)
	}

	if b.Name != ref.Name {
		return catalog.Blueprint{}, fmt.Errorf("%w: draft %s names %q", ErrCorrupt, ref, b.Name)
	}

	return b, nil
}

// Version returns the published version that r holds. It parses the document
// with catalog.ParseBlueprint, so a row that fails the rules of a blueprint
// is corrupt.
func Version(r VersionRow) (catalog.Version, error) {
	ref := catalog.BlueprintRef{Namespace: namespace.Name(r.Namespace), Name: r.Name}

	b, err := catalog.ParseBlueprint([]byte(r.DocumentJSON))
	if err != nil {
		return catalog.Version{}, fmt.Errorf("%w: %s version %d: %w", ErrCorrupt, ref, r.Number, err)
	}

	switch {
	case b.Name != r.Name:
		return catalog.Version{}, fmt.Errorf("%w: %s version %d names %q", ErrCorrupt, ref, r.Number, b.Name)
	case b.ExprVersion != "":
		return catalog.Version{}, fmt.Errorf("%w: %s version %d holds an Expr version in its document",
			ErrCorrupt, ref, r.Number)
	case r.Number < 1 || r.Number > catalog.MaxVersions:
		return catalog.Version{}, fmt.Errorf("%w: %s version %d", ErrCorrupt, ref, r.Number)
	case r.DraftVersion < 1:
		return catalog.Version{}, fmt.Errorf("%w: %s version %d has draft version %d",
			ErrCorrupt, ref, r.Number, r.DraftVersion)
	case r.Retired != 0 && r.Retired != 1:
		return catalog.Version{}, fmt.Errorf("%w: %s version %d has retired %d", ErrCorrupt, ref, r.Number, r.Retired)
	}

	b.ExprVersion = r.ExprVersion

	return catalog.Version{
		PublishedAt:  codec.FromMillis(r.PublishedAtMs),
		Blueprint:    b,
		Namespace:    namespace.Name(r.Namespace),
		PublishedBy:  r.PublishedBy,
		Number:       int(r.Number),
		DraftVersion: r.DraftVersion,
		Retired:      r.Retired == 1,
	}, nil
}

// CheckStoredDraft checks that the document of a publish is the stored draft
// of ref: both documents, without their Expr version, have the same SHA-256.
// It returns catalog.ErrInvalid for a document that differs, and ErrCorrupt
// for a stored draft that fails its checks.
//
// Defense in depth: the service reads the draft and publishes what it read.
// This check stays so that a caller cannot record a document that nobody
// wrote as a draft, and so that a version always has a draft behind it.
func CheckStoredDraft(ref catalog.BlueprintRef, stored string, b *catalog.Blueprint) error {
	draft, err := Draft(ref, stored)
	if err != nil {
		return err
	}

	want, err := documentHash(&draft)
	if err != nil {
		return err
	}

	got, err := documentHash(b)
	if err != nil {
		return err
	}

	if got != want {
		return fmt.Errorf("%w: the document is not the stored draft %s", catalog.ErrInvalid, ref)
	}

	return nil
}

// documentHash returns the SHA-256 of the JSON of b without its Expr version.
func documentHash(b *catalog.Blueprint) ([32]byte, error) {
	doc := *b
	doc.ExprVersion = ""

	data, err := json.Marshal(&doc)
	if err != nil {
		return [32]byte{}, fmt.Errorf("%w: encode blueprint: %w", catalog.ErrInvalid, err)
	}

	return sha256.Sum256(data), nil
}

// maxExprVersionBytes is the longest Expr version that a store keeps.
const maxExprVersionBytes = 64

// CheckExprVersion checks the form of an Expr version, for example v1.17.8: 1
// to 64 bytes of letters, digits, '.', '-', '+' and '_'. It returns
// catalog.ErrInvalid.
//
// Defense in depth: the service takes the version from the linked library.
// This check stays so that a new caller cannot write text that is not a version
// into a row that a backup carries.
func CheckExprVersion(v string) error {
	if v == "" || len(v) > maxExprVersionBytes {
		return fmt.Errorf("%w: Expr version of %d bytes", catalog.ErrInvalid, len(v))
	}

	for i := range len(v) {
		if !exprVersionByte(v[i]) {
			return fmt.Errorf("%w: Expr version has the byte %q", catalog.ErrInvalid, v[i])
		}
	}

	return nil
}

func exprVersionByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.IndexByte(".-+_", c) >= 0
}
