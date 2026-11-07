package provider

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"opendustpan.org/opendustpan/pkg/profile"
)

// Registry holds the provider adapters of an installation, one for each
// kind and name. The wiring makes it once at start.
type Registry struct {
	byUse map[string]entry
	now   func() time.Time
}

// NewRegistry returns a registry of ps. It returns profile.ErrConflict for two
// adapters with one kind and one name, and profile.ErrInvalid for a nil
// adapter, an adapter name that is not valid, an unknown kind or a spec that
// fails CheckSpec. now is the clock of Run; nil means time.Now.
func NewRegistry(now func() time.Time, ps ...Provider) (*Registry, error) {
	if now == nil {
		now = time.Now
	}

	r := &Registry{byUse: make(map[string]entry, len(ps)), now: now}

	for i, p := range ps {
		if p == nil {
			return nil, fmt.Errorf("%w: provider %d is nil", profile.ErrInvalid, i)
		}

		name, kind := p.Adapter(), p.Kind()
		if err := checkName(name); err != nil {
			return nil, fmt.Errorf("provider %d: %w", i, err)
		}

		if !kind.Valid() {
			return nil, fmt.Errorf("%w: provider %s has an unknown kind", profile.ErrInvalid, name)
		}

		spec := p.Spec()
		if err := CheckSpec(kind, &spec); err != nil {
			return nil, fmt.Errorf("provider %s/%s: %w", kind, name, err)
		}

		use := string(kind) + "/" + name
		if _, dup := r.byUse[use]; dup {
			return nil, fmt.Errorf("%w: two providers are %s", profile.ErrConflict, use)
		}

		r.byUse[use] = entry{p: p, spec: spec}
	}

	return r, nil
}

// entry is one adapter of the registry, with the spec that NewRegistry
// checked.
type entry struct {
	p    Provider
	spec Spec
}

// Spec returns the kind and the spec of the adapter of use, which is
// "<kind>/<name>", for example "login/oidc". The save check and the console
// read it. It returns ErrUnknownProvider, which wraps profile.ErrInvalid, for
// a use that names no adapter.
func (r *Registry) Spec(use string) (Kind, Spec, error) {
	e, err := r.lookup(use)
	if err != nil {
		return "", Spec{Inputs: nil, Outputs: nil, MintsTokens: false}, err
	}

	return e.p.Kind(), e.spec, nil
}

// Uses returns the use of each adapter, sorted.
func (r *Registry) Uses() []string {
	out := make([]string, 0, len(r.byUse))
	for use := range r.byUse {
		out = append(out, use)
	}

	slices.Sort(out)

	return out
}

func (r *Registry) lookup(use string) (entry, error) {
	kind, name, ok := strings.Cut(use, "/")
	if !ok || !Kind(kind).Valid() || checkName(name) != nil {
		return entry{}, fmt.Errorf("%w: %w: use is <kind>/<name>, with the kind login, secrets or outputs",
			profile.ErrInvalid, ErrUnknownProvider)
	}

	e, found := r.byUse[use]
	if !found {
		return entry{}, fmt.Errorf("%w: %w: %s", profile.ErrInvalid, ErrUnknownProvider, use)
	}

	return e, nil
}
