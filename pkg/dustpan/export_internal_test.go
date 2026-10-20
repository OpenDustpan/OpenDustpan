package dustpan

import "opendustpan.org/opendustpan/internal/api/apidesc"

// RouteSource is the list of the route patterns of one mounted adapter.
type RouteSource struct {
	Name     string
	Patterns []string
}

// Operations returns the joined operations of the adapters that the server
// mounts.
func (s *Server) Operations() []apidesc.Operation {
	if s.mounted == nil {
		return nil
	}

	return s.mounted.operations()
}

// RouteSources returns the route patterns of each mounted adapter.
func (s *Server) RouteSources() []RouteSource {
	if s.mounted == nil {
		return nil
	}

	sources := s.mounted.routeSources()
	out := make([]RouteSource, 0, len(sources))

	for _, source := range sources {
		out = append(out, RouteSource{Name: source.name, Patterns: source.patterns})
	}

	return out
}
