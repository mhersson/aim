package diffview

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestHighlighterCachesPerValue(t *testing.T) {
	lines := []string{"package main", "", "func main() {}"}

	var a, b Highlighter

	first := a.Highlight("main.go", lines)
	if first == nil {
		t.Fatal("main.go was not highlighted")
	}

	if got := a.Highlight("main.go", lines); !reflect.DeepEqual(got, first) {
		t.Errorf("second call = %v, want %v", got, first)
	}

	if _, ok := b.lexers.Load("main.go"); ok {
		t.Error("a lexer cached by one value is visible in another")
	}

	if _, ok := a.lexers.Load("main.go"); !ok {
		t.Error("the value that highlighted main.go did not cache its lexer")
	}

	if got := b.Highlight("main.go", lines); !reflect.DeepEqual(got, first) {
		t.Errorf("independent value = %v, want %v", got, first)
	}
}

func TestHighlighterUnknownAndHuge(t *testing.T) {
	var h Highlighter

	if h.Highlight("notes.unknownext", []string{"func x"}) != nil {
		t.Error("a file of no known type was highlighted")
	}

	if h.Highlight("big.go", []string{strings.Repeat("x", maxHighlightLine+1)}) != nil {
		t.Error("a line past the limit was tokenised")
	}

	if Enabled && h.Highlight("run", []string{"#!/bin/sh", "echo hi"}) == nil {
		t.Error("a script with a #! line was not highlighted")
	}
}

func TestHighlighterLang(t *testing.T) {
	var h Highlighter

	if h.HighlightLang("go", []string{"func main() {}"}) == nil {
		t.Error("a known language was not highlighted")
	}

	for i := range langMissMax + 2 {
		if h.HighlightLang(fmt.Sprint("nope-", i), []string{"x"}) != nil {
			t.Fatal("an unknown language was highlighted")
		}
	}

	h.missMu.Lock()
	n := len(h.misses)
	h.missMu.Unlock()

	if n == 0 || n > langMissMax {
		t.Errorf("%d misses kept, want between 1 and the cap %d", n, langMissMax)
	}
}

func TestHighlighterConcurrent(t *testing.T) {
	var h Highlighter

	done := make(chan struct{})

	for range 4 {
		go func() {
			defer func() { done <- struct{}{} }()

			for range 20 {
				h.Highlight("main.go", []string{"package main"})
				h.HighlightLang("py", []string{"def f(): pass"})
				h.HighlightLang("nope", []string{"x"})
			}
		}()
	}

	for range 4 {
		<-done
	}
}

// TestNoPackageVars keeps the package free of mutable state: the only
// package-level var is the embedded lexer definitions.
func TestNoPackageVars(t *testing.T) {
	var found []string

	for _, dir := range []string{".", "lexers"} {
		files, err := filepath.Glob(filepath.Join(dir, "*.go"))
		if err != nil {
			t.Fatal(err)
		}

		for _, file := range files {
			// gen.go is build-ignored and never part of the package.
			if strings.HasSuffix(file, "_test.go") || filepath.Base(file) == "gen.go" {
				continue
			}

			f, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
			if err != nil {
				t.Fatal(err)
			}

			for _, d := range f.Decls {
				gd, ok := d.(*ast.GenDecl)
				if !ok || gd.Tok != token.VAR {
					continue
				}

				for _, s := range gd.Specs {
					for _, n := range s.(*ast.ValueSpec).Names {
						if n.Name != "_" {
							found = append(found, file+": "+n.Name)
						}
					}
				}
			}
		}
	}

	want := []string{"lexers/registry.go: lexerFiles"}
	if !reflect.DeepEqual(found, want) {
		t.Errorf("package-level vars = %v, want %v", found, want)
	}
}
