package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"

	"opendustpan.org/opendustpan/pkg/registry"
)

// maxManifestBytes is the most bytes of the manifest file of a provider
// release, the terraform-registry-manifest.json file that the release tools
// publish next to the archives. The publish reads no more. A manifest of the
// release tools has less than 100 bytes.
const maxManifestBytes = 64 << 10

// manifestVersion is the only version of the manifest file that the publish
// reads.
const manifestVersion = 1

// manifestDocument is the shape of the manifest file. The manifest names its
// list protocol_versions, so the metadata is read without a struct.
type manifestDocument struct {
	Version  *int                       `json:"version"`
	Metadata map[string]json.RawMessage `json:"metadata"`
}

// manifestProtocols decodes the bytes of a manifest file and returns its
// protocol versions, sorted and without duplicates. The bytes come from
// outside the process. It returns registry.ErrInvalid for a body over the
// manifest bound, a body that is not one JSON object, a version other than 1,
// and a missing, empty or unknown protocol list.
func manifestProtocols(name string, data []byte) ([]string, error) {
	// Defense in depth: the measure step reads at most maxManifestBytes of a
	// manifest file. This check stays so that a new caller cannot send a
	// larger body to the decoder. It does not rely on the measure step.
	if len(data) > maxManifestBytes {
		return nil, fmt.Errorf(
			"%w: the manifest file %s is larger than %d bytes", registry.ErrInvalid, name, maxManifestBytes,
		)
	}

	dec := json.NewDecoder(bytes.NewReader(data))

	var doc manifestDocument
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("%w: the manifest file %s is not JSON of a manifest", registry.ErrInvalid, name)
	}

	if dec.More() {
		return nil, fmt.Errorf("%w: the manifest file %s holds more than one value", registry.ErrInvalid, name)
	}

	if doc.Version == nil || *doc.Version != manifestVersion {
		return nil, fmt.Errorf(
			"%w: the manifest file %s is not manifest version %d", registry.ErrInvalid, name, manifestVersion,
		)
	}

	var protocols []string

	listed, ok := doc.Metadata["protocol_versions"]
	if !ok || json.Unmarshal(listed, &protocols) != nil || len(protocols) == 0 {
		return nil, fmt.Errorf("%w: the manifest lists no protocol", registry.ErrInvalid)
	}

	// The plugin protocol versions that a manifest may list.
	known := []string{"5.0", "6.0"}

	for _, p := range protocols {
		if !slices.Contains(known, p) {
			return nil, fmt.Errorf("%w: protocol %q is not known: use 5.0 or 6.0", registry.ErrInvalid, p)
		}
	}

	slices.Sort(protocols)

	return slices.Compact(protocols), nil
}
