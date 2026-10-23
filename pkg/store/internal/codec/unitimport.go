package codec

import (
	"encoding/json"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/internal/unitimport"
	"opendustpan.org/opendustpan/pkg/history"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
)

// ImportRow is the column representation of an import. NamespaceID is the
// stable ID of the namespace. EndedAtMs is 0 for an import that did not end.
type ImportRow struct {
	NamespaceID string
	ID          string
	Kind        string
	Address     string
	Selection   string
	Credential  string
	Profile     string
	ProfilePath string
	Options     string
	State       string
	Reason      string
	CreatedBy   string
	CreatedAtMs int64
	Plan        string
	EndedAtMs   int64
}

type findingJSON struct {
	Code     string `json:"code"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
	Help     string `json:"help"`
}

type planJSON struct {
	AtMs         int64         `json:"atMs,omitempty"`
	Units        int           `json:"units"`
	Versions     int           `json:"versions"`
	Bytes        int64         `json:"bytes"`
	Capabilities []string      `json:"capabilities"`
	Findings     []findingJSON `json:"findings"`
	Requests     []string      `json:"requests"`
	Reservations []string      `json:"reservations"`
}

type optionsJSON struct {
	HistoryLimit int   `json:"historyLimit"`
	SinceMs      int64 `json:"sinceMs,omitempty"`
	LockSource   bool  `json:"lockSource"`
}

// optMillis encodes a time, with 0 for the zero time.
func optMillis(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}

	return Millis(t)
}

// optTime decodes optMillis.
func optTime(ms int64) time.Time {
	if ms == 0 {
		return time.Time{}
	}

	return FromMillis(ms)
}

func findingsToJSON(in []unitimport.Finding) []findingJSON {
	out := make([]findingJSON, 0, len(in))
	for _, f := range in {
		out = append(out, findingJSON{Code: f.Code, Severity: string(f.Severity), Message: f.Message, Help: f.Help})
	}

	return out
}

func findingsFromJSON(in []findingJSON) []unitimport.Finding {
	if len(in) == 0 {
		return nil
	}

	out := make([]unitimport.Finding, 0, len(in))
	for _, f := range in {
		out = append(out, unitimport.Finding{
			Code: f.Code, Severity: unitimport.Severity(f.Severity), Message: f.Message, Help: f.Help,
		})
	}

	return out
}

func nonNil(in []string) []string {
	if in == nil {
		return []string{}
	}

	return in
}

func orNil(in []string) []string {
	if len(in) == 0 {
		return nil
	}

	return in
}

// EncodePlan encodes a plan as the JSON text of its column.
func EncodePlan(p unitimport.Plan) (string, error) {
	caps := make([]string, 0, len(p.Capabilities))
	for _, c := range p.Capabilities {
		caps = append(caps, string(c))
	}

	b, err := json.Marshal(planJSON{
		AtMs: optMillis(p.At), Units: p.Units, Versions: p.Versions, Bytes: p.Bytes, Capabilities: caps,
		Findings: findingsToJSON(p.Findings), Requests: nonNil(p.Requests), Reservations: nonNil(p.Reservations),
	})
	if err != nil {
		return "", fmt.Errorf("encode import plan: %w", err)
	}

	return string(b), nil
}

// DecodePlan decodes the JSON text of a plan column.
func DecodePlan(s string) (unitimport.Plan, error) {
	var j planJSON
	if err := json.Unmarshal([]byte(s), &j); err != nil {
		return unitimport.Plan{}, fmt.Errorf("decode import plan: %w", err)
	}

	var caps []unitimport.Capability

	for _, c := range j.Capabilities {
		caps = append(caps, unitimport.Capability(c))
	}

	return unitimport.Plan{
		At: optTime(j.AtMs), Units: j.Units, Versions: j.Versions, Bytes: j.Bytes, Capabilities: caps,
		Findings: findingsFromJSON(j.Findings), Requests: orNil(j.Requests), Reservations: orNil(j.Reservations),
	}, nil
}

// ImportToRow encodes imp. The row holds the state planning and an empty
// plan; the caller gives the stable ID of the namespace.
func ImportToRow(nsID string, imp unitimport.Import) (ImportRow, error) {
	sel, err := json.Marshal(nonNil(imp.Selection.Names))
	if err != nil {
		return ImportRow{}, fmt.Errorf("encode import selection: %w", err)
	}

	opts, err := json.Marshal(optionsJSON{
		HistoryLimit: imp.Options.HistoryLimit,
		SinceMs:      optMillis(imp.Options.Since),
		LockSource:   imp.Options.LockSource,
	})
	if err != nil {
		return ImportRow{}, fmt.Errorf("encode import options: %w", err)
	}

	plan, err := EncodePlan(imp.Plan)
	if err != nil {
		return ImportRow{}, err
	}

	return ImportRow{
		NamespaceID: nsID,
		ID:          imp.ID,
		Kind:        string(imp.Kind),
		Address:     imp.Address,
		Selection:   string(sel),
		Credential:  string(imp.Credential),
		Profile:     imp.Profile,
		ProfilePath: imp.ProfilePath,
		Options:     string(opts),
		State:       string(imp.State),
		Reason:      imp.Reason,
		CreatedBy:   imp.CreatedBy,
		CreatedAtMs: Millis(imp.CreatedAt),
		Plan:        plan,
		EndedAtMs:   optMillis(imp.EndedAt),
	}, nil
}

// ImportFromRow decodes a row of the namespace ns.
func ImportFromRow(ns namespace.Name, row ImportRow) (unitimport.Import, error) {
	var names []string
	if err := json.Unmarshal([]byte(row.Selection), &names); err != nil {
		return unitimport.Import{}, fmt.Errorf("decode import selection: %w", err)
	}

	var opts optionsJSON
	if err := json.Unmarshal([]byte(row.Options), &opts); err != nil {
		return unitimport.Import{}, fmt.Errorf("decode import options: %w", err)
	}

	plan, err := DecodePlan(row.Plan)
	if err != nil {
		return unitimport.Import{}, err
	}

	return unitimport.Import{
		ID:          row.ID,
		Namespace:   ns,
		Kind:        unitimport.SourceKind(row.Kind),
		Address:     row.Address,
		Selection:   unitimport.Selection{Names: orNil(names)},
		Credential:  unitimport.CredentialWay(row.Credential),
		Profile:     row.Profile,
		ProfilePath: row.ProfilePath,
		Options: unitimport.Options{
			HistoryLimit: opts.HistoryLimit, Since: optTime(opts.SinceMs), LockSource: opts.LockSource,
		},
		State:     unitimport.State(row.State),
		Reason:    row.Reason,
		CreatedBy: row.CreatedBy,
		CreatedAt: FromMillis(row.CreatedAtMs),
		Plan:      plan,
		EndedAt:   optTime(row.EndedAtMs),
	}, nil
}

// ImportUnitRow is the column representation of an import unit. The target
// namespace is the namespace of the import, so the row holds the tool and the
// ID of the target only, both empty before the target exists.
type ImportUnitRow struct {
	NamespaceID  string
	ImportID     string
	ID           string
	Position     int64
	SourceTool   string
	SourceName   string
	TargetName   string
	TargetTool   string
	TargetUnitID string
	State        string
	ErrorCode    string
	Versions     int64
	Committed    int64
	Bytes        int64
	Encryption   string
	Findings     string
}

// ImportUnitToRow encodes u, the position-th unit of the import.
func ImportUnitToRow(nsID, importID string, position int, u unitimport.ImportUnit) (ImportUnitRow, error) {
	findings, err := json.Marshal(findingsToJSON(u.Findings))
	if err != nil {
		return ImportUnitRow{}, fmt.Errorf("encode import unit findings: %w", err)
	}

	return ImportUnitRow{
		NamespaceID:  nsID,
		ImportID:     importID,
		ID:           u.ID,
		Position:     int64(position),
		SourceTool:   u.Source.Tool,
		SourceName:   u.Source.Name,
		TargetName:   u.TargetName,
		TargetTool:   u.Target.Tool,
		TargetUnitID: u.Target.ID,
		State:        string(u.State),
		ErrorCode:    u.ErrorCode,
		Versions:     int64(u.Versions),
		Committed:    int64(u.Committed),
		Bytes:        u.Bytes,
		Encryption:   u.Encryption,
		Findings:     string(findings),
	}, nil
}

// ImportUnitFromRow decodes a row of the namespace ns.
func ImportUnitFromRow(ns namespace.Name, row ImportUnitRow) (unitimport.ImportUnit, error) {
	var findings []findingJSON
	if err := json.Unmarshal([]byte(row.Findings), &findings); err != nil {
		return unitimport.ImportUnit{}, fmt.Errorf("decode import unit findings: %w", err)
	}

	var target history.Unit
	if row.TargetUnitID != "" {
		target = history.Unit{Namespace: ns, Tool: row.TargetTool, ID: row.TargetUnitID}
	}

	return unitimport.ImportUnit{
		ID:         row.ID,
		ImportID:   row.ImportID,
		Source:     unitimport.SourceUnit{Tool: row.SourceTool, Name: row.SourceName},
		TargetName: row.TargetName,
		Target:     target,
		State:      unitimport.UnitState(row.State),
		ErrorCode:  row.ErrorCode,
		Versions:   int(row.Versions),
		Committed:  int(row.Committed),
		Bytes:      row.Bytes,
		Encryption: row.Encryption,
		Findings:   findingsFromJSON(findings),
	}, nil
}

// ImportLedgerRow is the column representation of a ledger row.
type ImportLedgerRow struct {
	NamespaceID   string
	ImportID      string
	ImportUnitID  string
	Version       string
	SourceSha256  string
	StoredSha256  string
	ItemTool      string
	ItemUnitID    string
	ItemKind      string
	ItemID        string
	CommittedAtMs int64
}

// LedgerToRow encodes r. The namespace of the history item is the namespace
// of the ledger row, so it is not a column.
func LedgerToRow(nsID string, r unitimport.LedgerRow) ImportLedgerRow {
	return ImportLedgerRow{
		NamespaceID:   nsID,
		ImportID:      r.ImportID,
		ImportUnitID:  r.ImportUnitID,
		Version:       string(r.Version),
		SourceSha256:  r.SourceSHA256,
		StoredSha256:  r.StoredSHA256,
		ItemTool:      r.Item.Unit.Tool,
		ItemUnitID:    r.Item.Unit.ID,
		ItemKind:      string(r.Item.Kind),
		ItemID:        r.Item.Item,
		CommittedAtMs: Millis(r.CommittedAt),
	}
}

// LedgerFromRow decodes a row of the namespace ns.
func LedgerFromRow(ns namespace.Name, row ImportLedgerRow) unitimport.LedgerRow {
	return unitimport.LedgerRow{
		ImportID:     row.ImportID,
		ImportUnitID: row.ImportUnitID,
		Version:      unitimport.VersionID(row.Version),
		SourceSHA256: row.SourceSha256,
		StoredSHA256: row.StoredSha256,
		Item: history.ItemRef{
			Unit: history.Unit{Namespace: ns, Tool: row.ItemTool, ID: row.ItemUnitID},
			Kind: secret.ItemKind(row.ItemKind),
			Item: row.ItemID,
		},
		CommittedAt: FromMillis(row.CommittedAtMs),
	}
}
