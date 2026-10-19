package profile

import (
	"regexp"
	"strings"

	"go.yaml.in/yaml/v3"
)

var (
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
// read. A scalar with an expression is scanned whole, because a "}" inside a
// string in the expression ends a match of "${...}" early. A scalar that
// reads $env may build the name of a key, so it reads the whole values tree.
func refsInText(text string) [][]string {
	if !strings.Contains(text, "${") {
		return nil
	}

	var out [][]string

	for _, m := range valuesRef.FindAllStringSubmatch(text, -1) {
		var segs []string

		for _, s := range pathSegment.FindAllStringSubmatch(m[1], -1) {
			segs = append(segs, s[1]+s[2]+s[3])
		}

		out = append(out, segs)
	}

	if strings.Contains(text, "$env") {
		out = append(out, []string{})
	}

	return out
}
