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

// HandlerNames returns the names of the adapters that the server mounts.
func (s *Server) HandlerNames() []string {
	if s.mounted == nil {
		return nil
	}

	var out []string

	for _, h := range s.mounted.handlers() {
		out = append(out, h.name)
	}

	return out
}

// RootPatterns is what the root mux recorded.
type RootPatterns struct {
	// Direct holds the patterns that code registered directly.
	Direct []string
	// Mounted holds the paths that mountV1 added from the patterns of an
	// adapter.
	Mounted []string
}

// RootPatterns returns the patterns that the root mux recorded.
func (s *Server) RootPatterns() RootPatterns {
	if s.root == nil {
		return RootPatterns{Direct: nil, Mounted: nil}
	}

	return RootPatterns{
		Direct:  append([]string(nil), s.root.patterns...),
		Mounted: append([]string(nil), s.root.mounted...),
	}
}

// AllHandlerNames returns the names of every adapter slot, mounted or not.
func (s *Server) AllHandlerNames() []string {
	if s.mounted == nil {
		return nil
	}

	var out []string

	for _, h := range s.mounted.allHandlers() {
		out = append(out, h.name)
	}

	return out
}
