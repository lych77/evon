// Copyright (c) 2020, lych77
// All rights reserved.
//
// Redistribution and use in source and binary forms, with or without
// modification, are permitted provided that the following conditions are met:
//
// 1. Redistributions of source code must retain the above copyright notice, this
//    list of conditions and the following disclaimer.
//
// 2. Redistributions in binary form must reproduce the above copyright notice,
//    this list of conditions and the following disclaimer in the documentation
//    and/or other materials provided with the distribution.
//
// 3. Neither the name of the copyright holder nor the names of its
//    contributors may be used to endorse or promote products derived from
//    this software without specific prior written permission.
//
// THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS "AS IS"
// AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT LIMITED TO, THE
// IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR A PARTICULAR PURPOSE ARE
// DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT HOLDER OR CONTRIBUTORS BE LIABLE
// FOR ANY DIRECT, INDIRECT, INCIDENTAL, SPECIAL, EXEMPLARY, OR CONSEQUENTIAL
// DAMAGES (INCLUDING, BUT NOT LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS OR
// SERVICES; LOSS OF USE, DATA, OR PROFITS; OR BUSINESS INTERRUPTION) HOWEVER
// CAUSED AND ON ANY THEORY OF LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY,
// OR TORT (INCLUDING NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE
// OF THIS SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.

package main

import (
	"flag"
	"fmt"
	"go/ast"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/text/width"
	"golang.org/x/tools/go/packages"
)

var (
	flagHandlerSuffix = flag.String("handler_suffix", "Handler", "Required suffix of the event handler type names")
	flagEventSuffix   = flag.String("event_suffix", "Event", "Suffix of the generated event type names")
	flagOut           = flag.String("out", "evon_gen.go", `Output source file name`)
	flagTags          = flag.String("tags", "", `Comma-separated Go build tags`)
	flagShow          = flag.Bool("show", false, "Show event handler types without generation")
)

func main() {
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "Usage: %s [flags] [dir]\nFlags:\n", os.Args[0])
		flag.PrintDefaults()
	}
	flag.Parse()

	dir, err := filepath.Abs(flag.Arg(0))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Fatal: %s\n", err)
		return
	}

	cfg := &packages.Config{
		Mode:       packages.NeedName | packages.NeedImports | packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo | packages.NeedDeps,
		Dir:        dir,
		BuildFlags: []string{"-tags=" + *flagTags},
	}

	pkgs, err := packages.Load(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Fatal: %s\n", err)
		return
	}

	if !process(pkgs[0], filepath.Join(dir, *flagOut)) {
		os.Exit(1)
	}
}

func process(pkg *packages.Package, path string) bool {
	for _, e := range pkg.Errors {
		if !strings.HasPrefix(e.Pos, path) {
			fmt.Fprintf(os.Stderr, "[go] %s\n", e)
		}
	}

	par := newParser(pkg)
	par.ParsePkg()

	if len(par.Errors) > 0 {
		for _, e := range par.Errors {
			fmt.Fprintf(os.Stderr, "[evon] %s\n", e)
		}
		return false
	}

	if len(par.Decls) == 0 {
		fmt.Println("(No handler types detected)")
		if !*flagShow {
			os.Remove(path)
		}
		return true
	}

	if *flagShow {
		showSummary(par)
		return true
	}

	return generate(par, path)
}

func showSummary(par *parser) {
	type showRow struct {
		Kind      string
		Name      *ast.Ident
		NameWidth int
		Flags     string
	}

	rows := []*showRow{}
	maxNameWidth := 0
	maxFlagsWidth := 0

	for _, decl := range par.Decls {
		flags := decl.Ann.FormatFlags()
		if len(flags) > maxFlagsWidth {
			maxFlagsWidth = len(flags)
		}

		row := &showRow{
			Kind:      "I",
			Name:      decl.Event.Name,
			NameWidth: monospaceLen(decl.Event.Name.Name),
			Flags:     flags,
		}

		if decl.Event.Funcs[0].Name == "" {
			row.Kind = "F"
		}

		if row.NameWidth > maxNameWidth {
			maxNameWidth = row.NameWidth
		}

		rows = append(rows, row)
	}

	for _, r := range rows {
		fmt.Printf("%s %s %s %s\n", r.Kind,
			r.Name.Name+strings.Repeat(" ", maxNameWidth-r.NameWidth),
			r.Flags+strings.Repeat(" ", maxFlagsWidth-len(r.Flags)),
			par.Pkg.Fset.Position(r.Name.NamePos))
	}
}

func monospaceLen(s string) int {
	res := 0
	for _, ch := range s {
		res++
		switch width.LookupRune(ch).Kind() {
		case width.EastAsianWide, width.EastAsianFullwidth:
			res++
		}
	}
	return res
}
