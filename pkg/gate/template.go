package gate

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

var templatePattern = regexp.MustCompile(`\{\{\s*([A-Za-z0-9_.]+)\s*\}\}`)

// factValues returns the fact document as nested maps.
func factValues(f Facts) (map[string]any, error) {
	raw, err := json.Marshal(f)
	if err != nil {
		return nil, fmt.Errorf("marshal facts: %w", err)
	}

	var doc map[string]any

	err = json.Unmarshal(raw, &doc)
	if err != nil {
		return nil, fmt.Errorf("read facts: %w", err)
	}

	return doc, nil
}

func lookup(doc map[string]any, path string) (any, bool) {
	var cur any = doc

	for _, part := range strings.Split(path, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}

		cur, ok = m[part]
		if !ok {
			return nil, false
		}
	}

	return cur, true
}

// renderMessage fills the {{fact.path}} fields of a message with the typed
// values of the facts. A path that is not a fact is an error: the template
// fails closed, and the caller stops the change with ErrRuleFailed. A fact
// that exists always has a value of its type (cost.available is false when
// there is no cost), so a missing value is never an empty string.
func renderMessage(msg string, f Facts) (string, error) {
	if !strings.Contains(msg, "{{") {
		return msg, nil
	}

	doc, err := factValues(f)
	if err != nil {
		return "", err
	}

	var missing []string

	out := templatePattern.ReplaceAllStringFunc(msg, func(m string) string {
		path := templatePattern.FindStringSubmatch(m)[1]

		v, ok := lookup(doc, path)
		if !ok {
			missing = append(missing, path)

			return m
		}

		return fmt.Sprint(v)
	})

	if len(missing) > 0 {
		return "", fmt.Errorf("%w: message names facts that do not exist: %s", ErrInvalid, strings.Join(missing, ", "))
	}

	return out, nil
}
