package expr

import "strings"

// isNodeLimit reports whether a compiler message is the node limit.
func isNodeLimit(msg string) bool {
	return strings.Contains(msg, "exceeds maximum allowed nodes")
}

// firstLine returns the first line of msg. The library adds the source and a
// marker under it.
func firstLine(msg string) string {
	line, _, _ := strings.Cut(msg, "\n")

	return line
}
