package issuersvc_test

import (
	"encoding/json"
	"slices"

	"opendustpan.org/opendustpan/pkg/notify"
)

func jsonUnmarshal(raw []byte, into any) error { return json.Unmarshal(raw, into) }

func jsonString(v any) string {
	raw, _ := json.Marshal(v)

	return string(raw)
}

func slicesContains(types []notify.Type, want notify.Type) bool { return slices.Contains(types, want) }
