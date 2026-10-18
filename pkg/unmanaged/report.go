package unmanaged

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"
)

// The sweep report format. A sweep report is JSON Lines: one header line, then
// one record line for each resource that the sweep saw. The runner uploads it
// as a stream, so its size does not need memory in the server.
const (
	// ReportFormat is the value of the field "format" of the header.
	ReportFormat = "opendustpan-sweep-report"
	// ReportVersion is the newest version of the format that this server
	// reads.
	ReportVersion = 1
	// MaxReportBytes bounds the size of one report.
	MaxReportBytes = 1 << 30
	// MaxReportRecords bounds the records of one report.
	MaxReportRecords = 5_000_000
	// MaxLineBytes bounds one line of a report.
	MaxLineBytes = 64 << 10
	// MaxReportScopes bounds the scopes of a header and of a sweep target.
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

// ReportHeader is the first line of a sweep report. Scopes says what the sweep
// covered. A scope with Complete false had an error, for example a region
// that refused the credentials: the server does not resolve a finding of
// that scope because the sweep did not see its resource.
type ReportHeader struct {
	Format  string        `json:"format"`
	Scopes  []ReportScope `json:"scopes"`
	Version int           `json:"version"`
}

// ReportScope is one account scope that a sweep covered: an AWS account, an
// Azure subscription, a Google Cloud project or a Kubernetes cluster, with
// the regions it read. An empty Regions means every region.
type ReportScope struct {
	Provider string   `json:"provider"`
	Scope    string   `json:"scope"`
	Error    string   `json:"error,omitempty"`
	Regions  []string `json:"regions,omitempty"`
	Complete bool     `json:"complete"`
}

// Record is one resource that a sweep saw. Type is the cloud's own type
// name, for example "AWS::S3::Bucket" or "Microsoft.Storage/storageAccounts".
// Region is empty for a global resource, for example an IAM role. Scope is
// the account scope of the resource. It is needed only when the key names no
// account scope of its own (the ARN of an S3 bucket) and the header has more
// than one scope of the provider; RecordScope gives the rule.
type Record struct {
	Tags     map[string]string `json:"tags,omitempty"`
	Provider string            `json:"provider"`
	ID       string            `json:"id"`
	Type     string            `json:"type"`
	Region   string            `json:"region,omitempty"`
	Name     string            `json:"name,omitempty"`
	Scope    string            `json:"scope,omitempty"`
}

// Key returns the normalized resource key of r. It returns ErrInvalidKey.
func (r Record) Key() (ResourceKey, error) {
	return NormalizeKey(ResourceKey{Provider: r.Provider, ID: r.ID})
}

// ParseHeader reads the header line of a report. It refuses an unknown
// field, and a scope that comes twice: one provider and account scope is one
// scope, with one Complete and one list of regions, so a complete copy cannot
// hide a copy that failed. It returns ErrInvalidReport.
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

	if err := checkUniqueScopes(h.Scopes); err != nil {
		return ReportHeader{}, err
	}

	return h, nil
}

// checkUniqueScopes reports whether each provider and account scope comes at
// most one time in scopes. It returns ErrInvalidReport.
func checkUniqueScopes(scopes []ReportScope) error {
	seen := make(map[[2]string]bool, len(scopes))

	for _, s := range scopes {
		id := [2]string{s.Provider, s.Scope}
		if seen[id] {
			return fmt.Errorf("%w: scope %q of provider %q comes twice", ErrInvalidReport, s.Scope, s.Provider)
		}

		seen[id] = true
	}

	return nil
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

// ParseRecord reads one record line of a report and normalizes its key. A
// record with no region takes the region of its key (Region). It refuses an
// unknown field. It returns ErrInvalidReport, which wraps
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

	if r.Type == "" || !shortText(r.Type) || !shortText(r.Region) || !shortText(r.Name) || !shortText(r.Scope) {
		return Record{}, ResourceKey{}, fmt.Errorf("%w: type, region or name", ErrInvalidReport)
	}

	if err := checkTags(r.Tags); err != nil {
		return Record{}, ResourceKey{}, err
	}

	r.ID = key.ID
	if r.Region == "" {
		r.Region = Region(key)
	}

	return r, key, nil
}

// CheckHeaderScopes reports whether each scope of the header of a report is
// a scope of its target: the same provider and account scope, and, when the
// target scope lists regions, only regions that it lists. A report whose
// header names any other scope, or names a scope twice, is refused as a
// whole, so a runner cannot widen its sweep beyond its target. It returns
// ErrInvalidReport.
func CheckHeaderScopes(target []TargetScope, header []ReportScope) error {
	if err := checkUniqueScopes(header); err != nil {
		return err
	}

	for _, h := range header {
		i := slices.IndexFunc(
			target,
			func(t TargetScope) bool { return t.Provider == h.Provider && t.Scope == h.Scope },
		)
		if i < 0 {
			return fmt.Errorf("%w: scope %q of provider %q is not a scope of the target", ErrInvalidReport, h.Scope,
				h.Provider)
		}

		allowed := target[i].Regions
		if len(allowed) == 0 {
			continue
		}

		if len(h.Regions) == 0 {
			return fmt.Errorf("%w: scope %q reads every region; the target reads %v", ErrInvalidReport, h.Scope,
				allowed)
		}

		for _, r := range h.Regions {
			if !slices.Contains(allowed, r) {
				return fmt.Errorf("%w: region %q of scope %q is not a region of the target", ErrInvalidReport, r,
					h.Scope)
			}
		}
	}

	return nil
}

// RecordScope returns the scope of the header that record r with the
// normalized key k belongs to, and false when r is outside every scope of the
// header. The match drops a record outside the header and counts it in
// ScanCounts.OutOfScope. The rules:
//
//   - The provider of r is the provider of the scope.
//   - A key with an account scope of its own (Scope) belongs to the scope of
//     that name. A record whose Scope names another scope is outside.
//   - A key with no account scope, for example the ARN of an S3 bucket,
//     belongs to the scope that the record names, or, when the record names
//     none, to the one scope of its provider in the header. With two or more
//     such scopes and no name, the record is outside.
//   - The region of a record is the region of its key (Region), else the
//     region that the record names. A record whose region is not the region
//     of its key is outside, as a record that names another account.
//   - When the scope lists regions, a record with a region belongs to it only
//     with one of them. A record with no region is a global resource, for
//     example an IAM role, and belongs to the scope.
//
// So a finding of a key with no account or region of its own takes the scope
// of the sweep that saw it.
func RecordScope(header []ReportScope, r Record, k ResourceKey) (ReportScope, bool) {
	none := ReportScope{Provider: "", Scope: "", Error: "", Regions: nil, Complete: false}

	want, ok := recordAccount(r, k)
	if !ok {
		return none, false
	}

	region, ok := recordRegion(r, k)
	if !ok {
		return none, false
	}

	var match []ReportScope

	for _, h := range header {
		if h.Provider == k.Provider && (want == "" || h.Scope == want) {
			match = append(match, h)
		}
	}

	if len(match) != 1 {
		return none, false
	}

	sc := match[0]
	if region != "" && len(sc.Regions) > 0 && !slices.Contains(sc.Regions, region) {
		return none, false
	}

	return sc, true
}

// recordAccount returns the account scope that record r with key k names:
// the scope of the key, else the scope of the record, else empty. It returns
// false when the record names another scope than its key.
func recordAccount(r Record, k ResourceKey) (string, bool) {
	own := Scope(k)

	switch {
	case own == "":
		return r.Scope, true
	case r.Scope != "" && r.Scope != own:
		return "", false
	default:
		return own, true
	}
}

// recordRegion returns the region that record r with key k names: the region
// of the key, else the region of the record, else empty. It returns false
// when the record names another region than its key.
func recordRegion(r Record, k ResourceKey) (string, bool) {
	own := Region(k)

	switch {
	case own == "":
		return r.Region, true
	case r.Region != "" && r.Region != own:
		return "", false
	default:
		return own, true
	}
}

// Covers reports whether the complete scope s of a sweep covers finding f, so
// that a sweep that did not see the resource of f resolves it. s must be
// complete, of the provider and the account scope of f, and, when s lists
// regions, f must have one of them. A finding with no region, a global
// resource, is covered only by a scope of every region. A finding with no
// scope is never covered.
func Covers(s ReportScope, f Finding) bool {
	switch {
	case !s.Complete || f.Scope == "" || s.Provider != f.Key.Provider || s.Scope != f.Scope:
		return false
	case len(s.Regions) == 0:
		return true
	default:
		return f.Region != "" && slices.Contains(s.Regions, f.Region)
	}
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
