package webhook

import (
	"encoding/json"
	"fmt"
	"maps"

	"opendustpan.org/opendustpan/pkg/notify"
)

// SchemaVersion is the value of the field schemaVersion of the body. A new
// field is an addition. A change of a field gets a new version.
const SchemaVersion = 1

// body is the JSON of a notification.
type body struct {
	SchemaVersion int               `json:"schemaVersion"`
	ID            string            `json:"id"`
	Type          notify.Type       `json:"type"`
	Scope         string            `json:"scope"`
	Object        string            `json:"object"`
	ObjectID      string            `json:"objectId"` //nolint:tagliatelle // The wire name of the field is objectId.
	Subject       string            `json:"subject"`
	Agent         string            `json:"agent"`
	Severity      string            `json:"severity"`
	Summary       string            `json:"summary"`
	Links         []link            `json:"links"`
	Attributes    map[string]string `json:"attributes"`
	DedupeKey     string            `json:"dedupeKey"`
	At            int64             `json:"at"`
	Sequence      int64             `json:"sequence"`
}

type link struct {
	Title string          `json:"title"`
	Kind  notify.LinkKind `json:"kind"`
	URL   string          `json:"url"`
}

// Encode returns the JSON body of n: the fields schemaVersion (1), id, type,
// scope, object, objectId, subject, agent, severity, summary, links,
// attributes, dedupeKey, at (Unix milliseconds) and sequence. links is an
// array and attributes is an object, also when empty. The body never holds
// the recipients of n. The keys of attributes are in order, so equal
// notifications give equal bytes.
func Encode(n notify.Notification) ([]byte, error) {
	links := make([]link, 0, len(n.Links))
	for _, l := range n.Links {
		links = append(links, link{Title: l.Title, Kind: l.Kind, URL: l.URL})
	}

	attrs := make(map[string]string, len(n.Attributes))
	maps.Copy(attrs, n.Attributes)

	b, err := json.Marshal(body{
		SchemaVersion: SchemaVersion,
		ID:            n.ID,
		Type:          n.Type,
		Scope:         n.Scope.String(),
		Object:        n.Object,
		ObjectID:      n.ObjectID,
		Subject:       n.Subject,
		Agent:         n.Agent,
		Severity:      n.Severity.String(),
		Summary:       n.Summary,
		Links:         links,
		Attributes:    attrs,
		DedupeKey:     n.DedupeKey,
		At:            n.At.UnixMilli(),
		Sequence:      n.Sequence,
	})
	if err != nil {
		return nil, fmt.Errorf("encode notification: %w", err)
	}

	return b, nil
}
