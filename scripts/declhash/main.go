//go:build ignore

// Command declhash prints one line for each top-level declaration (each spec
// of a grouped var, const or type) of the Go files it gets: the name and the
// SHA-256 of its source text with its doc comment, with runs of white space
// made one space. Each -r old=new argument rewrites the text before the hash.
//
// go run scripts/declhash/main.go [-r old=new]... FILE.go...
package main

import (
	"crypto/sha256"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"regexp"
	"sort"
	"strings"
)

func main() {
	var renames [][2]string
	var files []string
	args := os.Args[1:]
	for i := 0; i < len(args); i++ {
		if args[i] == "-r" {
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "declhash: -r needs a value of the form old=new")
				os.Exit(2)
			}
			kv := strings.SplitN(args[i+1], "=", 2)
			if len(kv) != 2 {
				fmt.Fprintf(os.Stderr, "declhash: -r value %q has no =, want old=new\n", args[i+1])
				os.Exit(2)
			}
			renames = append(renames, [2]string{kv[0], kv[1]})
			i++
			continue
		}
		files = append(files, args[i])
	}
	ws := regexp.MustCompile(`\s+`)
	var lines []string
	for _, path := range files {
		src, err := os.ReadFile(path)
		if err != nil {
			panic(err)
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, path, src, parser.ParseComments)
		if err != nil {
			panic(err)
		}
		text := func(doc *ast.CommentGroup, from, to token.Pos) string {
			if doc != nil {
				from = doc.Pos()
			}
			s := string(src[fset.Position(from).Offset:fset.Position(to).Offset])
			for _, r := range renames {
				s = strings.ReplaceAll(s, r[0], r[1])
			}
			return strings.TrimSpace(ws.ReplaceAllString(s, " "))
		}
		emit := func(name, s string) {
			lines = append(lines, fmt.Sprintf("%x %s", sha256.Sum256([]byte(s)), name))
		}
		for _, d := range f.Decls {
			switch d := d.(type) {
			case *ast.FuncDecl:
				name := d.Name.Name
				if d.Recv != nil {
					name = "(" + string(src[fset.Position(d.Recv.List[0].Type.Pos()).Offset:fset.Position(d.Recv.List[0].Type.End()).Offset]) + ")." + name
				}
				emit("func "+name, text(d.Doc, d.Pos(), d.End()))
			case *ast.GenDecl:
				if d.Tok == token.IMPORT {
					continue
				}
				for _, sp := range d.Specs {
					var name string
					var doc *ast.CommentGroup
					switch s := sp.(type) {
					case *ast.TypeSpec:
						name, doc = s.Name.Name, s.Doc
					case *ast.ValueSpec:
						name, doc = s.Names[0].Name, s.Doc
					}
					if !d.Lparen.IsValid() {
						doc = d.Doc
					}
					emit(d.Tok.String()+" "+name, text(doc, sp.Pos(), sp.End()))
				}
			}
		}
	}
	sort.Slice(lines, func(i, j int) bool { return lines[i][65:] < lines[j][65:] })
	fmt.Println(strings.Join(lines, "\n"))
}
