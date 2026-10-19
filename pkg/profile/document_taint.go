package profile

import (
	"regexp"

	"go.yaml.in/yaml/v3"
)

var (
	exprRef     = regexp.MustCompile(`\$\{[^}]*\}`)
	valuesRef   = regexp.MustCompile(`\bvalues((?:\.[A-Za-z_][A-Za-z0-9_-]*|\[\s*"[^"]*"\s*\]|\[\s*'[^']*'\s*\])*)`)
	pathSegment = regexp.MustCompile(`\.([A-Za-z_][A-Za-z0-9_-]*)|\[\s*"([^"]*)"\s*\]|\[\s*'([^']*)'\s*\]`)
)

// refsIn returns the paths under values that the expressions in n read. A
// computed index stops the path, so the path then names the whole subtree.
func refsIn(n *yaml.Node) [][]string {
	var out [][]string

	if n.Kind == yaml.ScalarNode {
		out = append(out, refsInText(n.Value)...)
	}

	for _, c := range n.Content {
		out = append(out, refsIn(c)...)
	}

	return out
}

// refsInText returns the paths under values that the expressions in text
// read.
func refsInText(text string) [][]string {
	var out [][]string

	for _, expr := range exprRef.FindAllString(text, -1) {
		for _, m := range valuesRef.FindAllStringSubmatch(expr, -1) {
			var segs []string

			for _, s := range pathSegment.FindAllStringSubmatch(m[1], -1) {
				segs = append(segs, s[1]+s[2]+s[3])
			}

			out = append(out, segs)
		}
	}

	return out
}
