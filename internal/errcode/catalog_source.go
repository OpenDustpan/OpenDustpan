package errcode

// The codes of the source filter. A code is never removed and never changes
// its meaning.
const (
	// SourceFilterInvalid means that the source filter in the request fails
	// the checks of a filter.
	SourceFilterInvalid = "source.filter-invalid"
	// SourceFilterUnreadable means that the source filter that the server
	// stored for the namespace fails the checks of a filter.
	SourceFilterUnreadable = "source.filter-unreadable"
)

// sourceSpecs are the codes of the source filter.
func sourceSpecs() []Spec {
	return []Spec{
		{
			Code:    SourceFilterInvalid,
			Meaning: "the source filter in the request fails the checks of a filter",
			Args:    nil,
		},
		{
			Code:    SourceFilterUnreadable,
			Meaning: "the source filter that the server stored for the namespace fails the checks of a filter",
			Args:    nil,
		},
	}
}
