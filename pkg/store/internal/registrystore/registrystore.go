// Package registrystore holds the rules of the registry release table that the
// SQLite and PostgreSQL adapters share: the JSON form of the archives, and the
// check of a stored row. A row is untrusted when it is read, because a backup,
// a restore or another version of the server can have written it.
package registrystore

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/registry"
)

// ErrCorrupt reports a stored row that does not pass the checks of a release.
// The adapters do not serve such a row.
var ErrCorrupt = errors.New("stored registry release is not valid")

// Row is one row of the release table, with the name of its namespace.
type Row struct {
	Namespace       string
	Kind            string
	Name            string
	System          string
	Version         string
	SumsFile        string
	SumsSHA256      string
	SignatureFile   string
	SignatureSHA256 string
	KeyFingerprint  string
	// Protocols holds the plugin protocol versions joined by commas, or the
	// empty text for a module.
	Protocols     string
	PublishedBy   string
	ArchivesJSON  string
	PublishedAtMs int64
	// StoredFilesJSON is the JSON of the digests of the stored files, or the
	// empty text for a release that an earlier version of the server stored.
	StoredFilesJSON string
}

type storedFileDoc struct {
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
	MD5    string `json:"md5"`
}

// EncodeFiles returns the JSON of the digests of the stored files of a
// release, or the empty text when the release has none.
func EncodeFiles(in []registry.StoredFile) (string, error) {
	if len(in) == 0 {
		return "", nil
	}

	docs := make([]storedFileDoc, 0, len(in))
	for _, f := range in {
		docs = append(docs, storedFileDoc(f))
	}

	b, err := json.Marshal(docs)
	if err != nil {
		return "", fmt.Errorf("encode registry stored files: %w", err)
	}

	return string(b), nil
}

// decodeFiles reads the JSON that EncodeFiles wrote. It refuses a field that
// it does not know and text after the document. The empty text is no digest.
func decodeFiles(s string) ([]registry.StoredFile, error) {
	if s == "" {
		return nil, nil
	}

	dec := json.NewDecoder(bytes.NewReader([]byte(s)))
	dec.DisallowUnknownFields()

	var docs []storedFileDoc
	if err := dec.Decode(&docs); err != nil {
		return nil, fmt.Errorf("%w: stored files: %w", ErrCorrupt, err)
	}

	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%w: stored files: text after the document", ErrCorrupt)
	}

	out := make([]registry.StoredFile, 0, len(docs))
	for _, d := range docs {
		out = append(out, registry.StoredFile(d))
	}

	return out, nil
}

type archiveDoc struct {
	Platform string `json:"platform"`
	FileName string `json:"fileName"`
	SHA256   string `json:"sha256"`
	Size     int64  `json:"size"`
}

// EncodeArchives returns the JSON of the archives of a release. The list keeps
// its order.
func EncodeArchives(in []registry.Archive) (string, error) {
	docs := make([]archiveDoc, 0, len(in))
	for _, a := range in {
		docs = append(docs, archiveDoc(a))
	}

	b, err := json.Marshal(docs)
	if err != nil {
		return "", fmt.Errorf("encode registry archives: %w", err)
	}

	return string(b), nil
}

// decodeArchives reads the JSON that EncodeArchives wrote. It refuses a field
// that it does not know and text after the document.
func decodeArchives(s string) ([]registry.Archive, error) {
	dec := json.NewDecoder(bytes.NewReader([]byte(s)))
	dec.DisallowUnknownFields()

	var docs []archiveDoc
	if err := dec.Decode(&docs); err != nil {
		return nil, fmt.Errorf("%w: archives: %w", ErrCorrupt, err)
	}

	// A second decode must reach the end of the text. dec.More does not say
	// this: it is false before a stray closing brace.
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%w: archives: text after the document", ErrCorrupt)
	}

	out := make([]registry.Archive, 0, len(docs))
	for _, d := range docs {
		out = append(out, registry.Archive(d))
	}

	return out, nil
}

// JoinProtocols returns the text form of the plugin protocol versions of a
// release: the versions joined by commas.
func JoinProtocols(protocols []string) string {
	return strings.Join(protocols, ",")
}

// splitProtocols reads the text that JoinProtocols wrote. The empty text is no
// protocol.
func splitProtocols(s string) []string {
	if s == "" {
		return nil
	}

	return strings.Split(s, ",")
}

// Release returns the release that row holds. It runs the checks of a new
// release again and returns ErrCorrupt for a row that fails them.
func Release(row Row) (registry.Release, error) {
	ns, err := namespace.ParseName(row.Namespace)
	if err != nil {
		return registry.Release{}, fmt.Errorf("%w: namespace: %w", ErrCorrupt, err)
	}

	archives, err := decodeArchives(row.ArchivesJSON)
	if err != nil {
		return registry.Release{}, err
	}

	files, err := decodeFiles(row.StoredFilesJSON)
	if err != nil {
		return registry.Release{}, err
	}

	rel := registry.Release{
		PublishedAt: time.UnixMilli(row.PublishedAtMs).UTC(),
		Address: registry.Address{
			Namespace: ns, Kind: registry.Kind(row.Kind), Name: row.Name, System: row.System,
		},
		Version:         row.Version,
		SumsFile:        row.SumsFile,
		SumsSHA256:      row.SumsSHA256,
		SignatureFile:   row.SignatureFile,
		SignatureSHA256: row.SignatureSHA256,
		KeyFingerprint:  row.KeyFingerprint,
		Protocols:       splitProtocols(row.Protocols),
		PublishedBy:     row.PublishedBy,
		Archives:        archives,
		Files:           files,
	}

	if err := rel.Validate(); err != nil {
		return registry.Release{}, fmt.Errorf("%w: %w", ErrCorrupt, err)
	}

	return rel, nil
}

// Address returns the address that the columns hold, checked as a new one.
func Address(ns, kind, name, system string) (registry.Address, error) {
	name2, err := namespace.ParseName(ns)
	if err != nil {
		return registry.Address{}, fmt.Errorf("%w: namespace: %w", ErrCorrupt, err)
	}

	addr := registry.Address{Namespace: name2, Kind: registry.Kind(kind), Name: name, System: system}
	if err := addr.Validate(); err != nil {
		return registry.Address{}, fmt.Errorf("%w: %w", ErrCorrupt, err)
	}

	return addr, nil
}

// KeyRow is one row of the key table, with the name of its namespace.
type KeyRow struct {
	Namespace   string
	Fingerprint string
	ArmoredKey  string
	AddedBy     string
	AddedAtMs   int64
}

// Key returns the key record that row holds. It runs the checks of a new key
// again and returns ErrCorrupt for a row that fails them.
func Key(row KeyRow) (registry.KeyRecord, error) {
	ns, err := namespace.ParseName(row.Namespace)
	if err != nil {
		return registry.KeyRecord{}, fmt.Errorf("%w: namespace: %w", ErrCorrupt, err)
	}

	rec := registry.KeyRecord{
		AddedAt:   time.UnixMilli(row.AddedAtMs).UTC(),
		Namespace: ns,
		Key:       registry.TrustedKey{Fingerprint: row.Fingerprint, Armored: []byte(row.ArmoredKey)},
		AddedBy:   row.AddedBy,
	}

	if err := rec.Validate(); err != nil {
		return registry.KeyRecord{}, fmt.Errorf("%w: %w", ErrCorrupt, err)
	}

	return rec, nil
}
