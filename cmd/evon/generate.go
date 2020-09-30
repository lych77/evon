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
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/printer"
	"go/token"
	"os"
	"strings"
	"text/template"

	"golang.org/x/text/width"
	"golang.org/x/tools/go/packages"
)

func extractParamsArgs(list []*ast.Field, fset *token.FileSet, paramSet dedupSet) (string, string) {
	dummies := newDedupSet("_")
	for _, pg := range list {
		for _, n := range pg.Names {
			if n.Name != "_" {
				dummies.Resolve(n.Name)
			}
		}
	}

	params := []string{}
	args := []string{}

	for _, pg := range list {
		if len(pg.Names) == 0 {
			pg.Names = append(pg.Names, ast.NewIdent(dummies.Resolve("_")))
		}

		argGrp := []string{}
		for _, n := range pg.Names {
			if n.Name == "_" {
				n.Name = dummies.Resolve("_")
			}

			argGrp = append(argGrp, n.Name)
			args = append(args, n.Name)
			paramSet[n.Name] = true
		}

		typeBuf := &bytes.Buffer{}
		printer.Fprint(typeBuf, fset, pg.Type)
		params = append(params, strings.Join(argGrp, ", ")+" "+typeBuf.String())
	}

	argsStr := strings.Join(args, ", ")
	if len(list) > 0 {
		if _, ok := list[len(list)-1].Type.(*ast.Ellipsis); ok {
			argsStr += "..."
		}
	}

	return strings.Join(params, ", "), argsStr
}

func extractReturns(list []*ast.Field, fset *token.FileSet) string {
	returns := []string{}

	for _, pg := range list {
		blanks := "_"
		if len(pg.Names) > 1 {
			blanks += strings.Repeat(", _", len(pg.Names)-1)
		}
		typeBuf := &bytes.Buffer{}
		printer.Fprint(typeBuf, fset, pg.Type)
		returns = append(returns, blanks+" "+typeBuf.String())
	}

	return strings.Join(returns, ", ")
}

func process(pkg *packages.Package, path string) {
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
		return
	}

	if len(par.Decls) == 0 {
		fmt.Println("(No handler types detected)")
		if !*flagShow {
			os.Remove(path)
		}
		return
	}

	if *flagShow {
		showSummary(par)
	} else {
		generate(par, path)
	}
}

type genFile struct {
	Package string
	Imports []*genImport
	Events  []*genEvent

	HandlerSuffix string
	EventSuffix   string

	SyncAlias      string
	SyncAliasLocal string
}

type genImport struct {
	Alias string
	Path  string
}

type genEvent struct {
	Name     string
	Flags    map[string]bool
	FlagsLit string
	Funcs    []*genFunc
	Dedups   map[string]string
}

type genFunc struct {
	Name    string
	Params  string
	Args    string
	Returns string
}

func generate(par *parser, path string) {
	file := &genFile{
		Package:       par.Pkg.Name,
		HandlerSuffix: *flagHandlerSuffix,
		EventSuffix:   *flagEventSuffix,
	}

	if rec, ok := par.Imports["sync"]; ok {
		file.SyncAlias = rec.Alias
		delete(par.PkgNameSet, file.SyncAlias)
	}

	for _, decl := range par.Decls {
		for _, ev := range decl.Events {
			paramSet := dedupSet{}

			gfs := []*genFunc{}
			for _, f := range ev.Funcs {
				gf := &genFunc{Name: f.Name}
				gf.Params, gf.Args = extractParamsArgs(f.Params, par.Pkg.Fset, paramSet)
				gf.Returns = extractReturns(f.Returns, par.Pkg.Fset)
				gfs = append(gfs, gf)
			}

			ge := &genEvent{
				Name:     ev.Name.Name[:len(ev.Name.Name)-len(*flagHandlerSuffix)],
				Flags:    decl.Ann.Flags,
				FlagsLit: decl.Ann.FormatFlags(),
				Funcs:    gfs,
				Dedups:   map[string]string{},
			}

			par.PkgNameSet.Merge(paramSet)

			for _, n := range localIdents {
				ge.Dedups[n] = paramSet.Resolve(n)
			}
			file.Events = append(file.Events, ge)
		}
	}

	for _, r := range par.ImportList {
		if r.Alias == r.Name {
			r.Alias = ""
		}
		file.Imports = append(file.Imports, &genImport{Alias: r.Alias, Path: r.Path})
	}

	if par.NeedSyncLocal {
		file.SyncAliasLocal = par.PkgNameSet.Resolve(file.SyncAlias)
		if file.SyncAliasLocal != file.SyncAlias {
			file.Imports = append(file.Imports, &genImport{Alias: file.SyncAliasLocal, Path: "sync"})
		}
	}

	writeFile(file, path)
}

func prefixIdent(p, s string) string {
	if token.IsExported(s) {
		return strings.Title(p) + strings.Title(s)
	}
	return strings.ToLower(p) + strings.Title(s)
}

func writeFile(file *genFile, path string) {
	tpl := template.Must(template.New("").Funcs(template.FuncMap{"prefix": prefixIdent}).Parse(templateText))

	buf := &bytes.Buffer{}
	err := tpl.Execute(buf, file)
	if err != nil {
		panic(err)
	}

	src, err := format.Source(buf.Bytes())
	if err != nil {
		panic(err)
	}

	outFile, err := os.Create(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Fatal: %s\n", err)
		return
	}
	defer outFile.Close()
	outFile.Write(src)

	fmt.Printf("Generated %s\n", path)
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

		for _, ev := range decl.Events {
			row := &showRow{
				Kind:      "I",
				Name:      ev.Name,
				NameWidth: monospaceLen(ev.Name.Name),
				Flags:     flags,
			}

			if ev.Funcs[0].Name == "" {
				row.Kind = "F"
			}

			if row.NameWidth > maxNameWidth {
				maxNameWidth = row.NameWidth
			}

			rows = append(rows, row)
		}
	}

	for _, r := range rows {
		fmt.Printf("%s %s %s %s\n", r.Kind,
			r.Name.Name+strings.Repeat(" ", maxNameWidth-r.NameWidth),
			r.Flags+strings.Repeat(" ", maxFlagsWidth-len(r.Flags)),
			par.Pkg.Fset.Position(r.Name.NamePos))
	}
}
