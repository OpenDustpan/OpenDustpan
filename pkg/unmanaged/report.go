package unmanaged

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"
)

// The scan report format. A scan report is JSON Lines: one header line, then
// one record line for each resource that the scan saw. The runner uploads it
// as a stream, so its size does not need memory in the server.
const (
	// ReportFormat is the value of the field "format" of the header.
	ReportFormat = "opendustpan-scan-report"
	// ReportVersion is the newest version of the format that this server
	// reads.
	ReportVersion = 1
	// MaxReportBytes bounds the size of one report.
	MaxReportBytes = 1 << 30
	// MaxReportRecords bounds the records of one report.
	MaxReportRecords = 5_000_000
	// MaxLineBytes bounds one line of a report.
	MaxLineBytes = 64 << 10
	// MaxReportScopes bounds the scopes of a header and of a scan target.
	MaxReportScopes = 1000
	// MaxTags bounds the tags of one record.
	MaxTags = 50
	// MaxTagKeyBytes bounds the key of a tag.
	MaxTagKeyBytes = 128
	// MaxTagValueBytes bounds the value of a tag.
	MaxTagValueBytes = 256
	// maxShortText bounds a type, a region, a name and a scope error.
	maxShortText = 512
)

// ReportHeader is the first line of a scan report. Scopes says what the scan
// covered. A scope with Complete false had an error, for example a region
// that refused the credentials: the server does not resolve a finding of
// that scope because the scan did not see its resource.
type ReportHeader struct {
	Format  string        `json:"format"`
	Scopes  []ReportScope `json:"scopes"`
	Version int           `json:"version"`
}

// ReportScope is one account scope that a scan covered: an AWS account, an
// Azure subscription, a Google Cloud project or a Kubernetes cluster, with
// the regions it read. An empty Regions means every region.
type ReportScope struct {
	Provider string   `json:"provider"`
	Scope    string   `json:"scope"`
	Error    string   `json:"error,omitempty"`
	Regions  []string `json:"regions,omitempty"`
	Complete bool     `json:"complete"`
}

// Record is one resource that a scan saw. Type is the cloud's own type
// name, for example "AWS::S3::Bucket" or "Microsoft.Storage/storageAccounts".
type Record struct {
	Tags     map[string]string `json:"tags,omitempty"`
	Provider string            `json:"provider"`
	ID       string            `json:"id"`
	Type     string            `json:"type"`
	Region   string            `json:"region,omitempty"`
	Name     string            `json:"name,omitempty"`
}

// Key returns the normalized resource key of r. It returns ErrInvalidKey.
func (r Record) Key() (ResourceKey, error) {
	return NormalizeKey(ResourceKey{Provider: r.Provider, ID: r.ID})
}

// ParseHeader reads the header line of a report. It refuses an unknown
// field. It returns ErrInvalidReport.
func ParseHeader(line []byte) (ReportHeader, error) {
	var h ReportHeader
	if err := decodeStrict(line, &h); err != nil {
		return ReportHeader{}, err
	}

	switch {
	case h.Format != ReportFormat:
		return ReportHeader{}, fmt.Errorf("%w: format %q", ErrInvalidReport, h.Format)
	case h.Version < 1 || h.Version > ReportVersion:
		return ReportHeader{}, fmt.Errorf("%w: version %d; this server reads version %d", ErrInvalidReport, h.Version,
			ReportVersion)
	case len(h.Scopes) == 0 || len(h.Scopes) > MaxReportScopes:
		return ReportHeader{}, fmt.Errorf("%w: 1 to %d scopes", ErrInvalidReport, MaxReportScopes)
	}

	for _, s := range h.Scopes {
		if err := checkReportScope(s); err != nil {
			return ReportHeader{}, err
		}
	}

	return h, nil
}

// checkReportScope reports whether s is a valid scope of a report header. It
// returns ErrInvalidReport.
func checkReportScope(s ReportScope) error {
	if s.Scope == "" || !shortText(s.Scope) || !shortText(s.Error) {
		return fmt.Errorf("%w: scope %q", ErrInvalidReport, s.Scope)
	}

	if !validProvider(s.Provider) {
		return fmt.Errorf("%w: provider %q", ErrInvalidReport, s.Provider)
	}

	if s.Complete && s.Error != "" {
		return fmt.Errorf("%w: a complete scope has no error", ErrInvalidReport)
	}

	return nil
}

// ParseRecord reads one record line of a report and normalizes its key. It
// refuses an unknown field. It returns ErrInvalidReport, which wraps
// ErrInvalidKey for an invalid key.
func ParseRecord(line []byte) (Record, ResourceKey, error) {
	var r Record
	if err := decodeStrict(line, &r); err != nil {
		return Record{}, ResourceKey{}, err
	}

	key, err := r.Key()
	if err != nil {
		return Record{}, ResourceKey{}, fmt.Errorf("%w: %w", ErrInvalidReport, err)
	}

	if r.Type == "" || !shortText(r.Type) || !shortText(r.Region) || !shortText(r.Name) {
		return Record{}, ResourceKey{}, fmt.Errorf("%w: type, region or name", ErrInvalidReport)
	}

	if err := checkTags(r.Tags); err != nil {
		return Record{}, ResourceKey{}, err
	}

	r.ID = key.ID

	return r, key, nil
}

// checkTags reports whether tags holds at most MaxTags tags, each with a key
// and a value within their bounds. It returns ErrInvalidReport.
func checkTags(tags map[string]string) error {
	if len(tags) > MaxTags {
		return fmt.Errorf("%w: more than %d tags", ErrInvalidReport, MaxTags)
	}

	for k, v := range tags {
		if k == "" || len(k) > MaxTagKeyBytes || len(v) > MaxTagValueBytes || !utf8.ValidString(k+v) {
			return fmt.Errorf("%w: tag %q", ErrInvalidReport, k)
		}
	}

	return nil
}

func decodeStrict(line []byte, v any) error {
	if len(line) > MaxLineBytes {
		return fmt.Errorf("%w: a line above %d bytes", ErrInvalidReport, MaxLineBytes)
	}

	dec := json.NewDecoder(bytes.NewReader(line))
	dec.DisallowUnknownFields()

	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("%w: not a JSON object with the known fields", ErrInvalidReport)
	}

	if dec.More() {
		return fmt.Errorf("%w: more than one JSON value on a line", ErrInvalidReport)
	}

	return nil
}

func shortText(s string) bool {
	return len(s) <= maxShortText && utf8.ValidString(s) && !strings.ContainsAny(s, "\n\r\t\x00")
}
