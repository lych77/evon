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
	"sort"
	"strings"
	"text/template"
)

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
	Name       string
	Sig        string
	Args       string
	HasResults bool
}

func generate(par *parser, path string) bool {
	file := &genFile{
		Package:       par.Pkg.Name,
		HandlerSuffix: *flagHandlerSuffix,
		EventSuffix:   *flagEventSuffix,
	}

	importList, pkgNameSet := dedupImports(par)

	for _, decl := range par.Decls {
		paramSet := newDedupSet()

		gfs := []*genFunc{}
		for _, f := range decl.Event.Funcs {
			gf := &genFunc{Name: f.Name}
			renderSignatureArgs(gf, f.Type, par.Pkg.Fset, paramSet)
			gfs = append(gfs, gf)
		}

		ge := &genEvent{
			Name:     decl.Event.Name.Name[:len(decl.Event.Name.Name)-len(*flagHandlerSuffix)],
			Flags:    decl.Ann.Flags,
			FlagsLit: decl.Ann.FormatFlags(),
			Funcs:    gfs,
			Dedups:   make(map[string]string),
		}

		pkgNameSet.Merge(paramSet)

		for _, n := range localIdents {
			ge.Dedups[n] = paramSet.Resolve(n)
		}
		file.Events = append(file.Events, ge)
	}

	for _, r := range importList {
		gi := &genImport{Path: r.Path}
		if r.Alias != r.Name {
			gi.Alias = r.Alias
		}
		file.Imports = append(file.Imports, gi)
	}

	if rec, ok := par.Imports["sync"]; ok {
		file.SyncAlias = rec.Alias
		if rec.Local {
			file.SyncAliasLocal = pkgNameSet.Resolve(file.SyncAlias)
			if file.SyncAliasLocal != file.SyncAlias {
				file.Imports = append(file.Imports, &genImport{Alias: file.SyncAliasLocal, Path: "sync"})
			}
		}
	}

	return writeFile(file, path)
}

func dedupImports(par *parser) ([]*importRec, dedupSet) {
	recs := []*importRec{}
	for _, r := range par.Imports {
		recs = append(recs, r)
	}
	sort.Slice(recs, func(i, j int) bool {
		iPrio := recs[i].Priority
		jPrio := recs[j].Priority
		return iPrio < jPrio || iPrio == jPrio && recs[i].Path < recs[j].Path
	})

	dedup := newDedupSet()
	for _, r := range recs {
		r.Alias = dedup.Resolve(r.Name)

		for id := range r.TypeIdents {
			id.Name = r.Alias + "." + id.Name
		}
		for id := range r.PkgIdents {
			id.Name = r.Alias
		}
	}

	return recs, dedup
}

func renderSignatureArgs(gf *genFunc, typ *ast.FuncType, fset *token.FileSet, allParamSet dedupSet) {
	paramSet := newDedupSet("_")
	for _, pg := range typ.Params.List {
		for _, n := range pg.Names {
			if n.Name != "_" {
				paramSet[n.Name] = true
			}
		}
	}

	args := []string{}

	for _, pg := range typ.Params.List {
		if len(pg.Names) == 0 {
			pg.Names = append(pg.Names, ast.NewIdent("_"))
		}

		for _, n := range pg.Names {
			if n.Name == "_" {
				n.Name = paramSet.Resolve("_")
			}

			args = append(args, n.Name)
			allParamSet[n.Name] = true
		}
	}

	gf.Args = strings.Join(args, ", ")
	if len(typ.Params.List) > 0 {
		if _, ok := typ.Params.List[len(typ.Params.List)-1].Type.(*ast.Ellipsis); ok {
			gf.Args += "..."
		}
	}

	if typ.Results != nil {
		for _, pg := range typ.Results.List {
			if len(pg.Names) == 0 {
				pg.Names = append(pg.Names, ast.NewIdent("_"))
			} else {
				for _, n := range pg.Names {
					n.Name = "_"
				}
			}
		}
		gf.HasResults = true
	}

	sigBuf := &bytes.Buffer{}
	printer.Fprint(sigBuf, fset, typ)
	gf.Sig = sigBuf.String()[4:]
}

func writeFile(file *genFile, path string) bool {
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
		return false
	}
	defer outFile.Close()
	outFile.Write(src)

	fmt.Printf("Generated %s\n", path)
	return true
}

func prefixIdent(p, s string) string {
	if token.IsExported(s) {
		return strings.Title(p) + strings.Title(s)
	}
	return strings.ToLower(p) + strings.Title(s)
}
