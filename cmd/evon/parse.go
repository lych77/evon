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
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"sort"
	"strings"

	"golang.org/x/tools/go/packages"
)

type parser struct {
	Pkg *packages.Package

	Decls  []*declRec
	Errors []error

	Imports    map[string]*importRec
	ImportList []*importRec

	NeedSync      bool
	NeedSyncLocal bool
	PkgNameSet    dedupSet

	resolver typeResolver
}

type declRec struct {
	Ann    *annotation
	Events []*eventRec
}

type eventRec struct {
	Name  *ast.Ident
	Funcs []*funcRec
}

type funcRec struct {
	Name    string
	Params  []*ast.Field
	Returns []*ast.Field
}

type importRec struct {
	Path       string
	Name       string
	Alias      string
	PkgIdents  []*ast.Ident
	TypeIdents []*ast.Ident
}

func newParser(pkg *packages.Package) *parser {
	return &parser{
		Pkg:      pkg,
		Imports:  map[string]*importRec{},
		resolver: make(typeResolver),
	}
}

func (par *parser) ParsePkg() {
	for _, f := range par.Pkg.Syntax {
		par.ParseFile(f)
	}

	if len(par.Errors) > 0 {
		return
	}

	par.DedupImports()
}

func (par *parser) ParseFile(file *ast.File) {
	type annRec struct {
		Decl    *declRec
		Errors  []error
		Visited bool
	}

	anns := []*annRec{}
	cmtAnns := map[*ast.CommentGroup]*annRec{}

	for _, cg := range file.Comments {
		ann, err := extractAnnotation(cg, par.Pkg.Fset)
		if err != nil {
			par.Errors = append(par.Errors, err)
			continue
		}

		if ann != nil {
			rec := &annRec{Decl: &declRec{Ann: ann}}
			anns = append(anns, rec)
			cmtAnns[cg] = rec

			par.NeedSyncLocal = par.NeedSyncLocal || ann.Flags[annWait]
			par.NeedSync = par.NeedSyncLocal || ann.Flags[annLock]
		}
	}

	for _, d := range file.Decls {
		gd, ok := d.(*ast.GenDecl)
		if !ok || gd.Tok != token.TYPE {
			continue
		}

		outerStack := []*annRec{}
		if ann, ok := cmtAnns[gd.Doc]; ok {
			outerStack = append(outerStack, ann)
		}

		for _, s := range gd.Specs {
			ts := s.(*ast.TypeSpec)

			innerStack := outerStack[:]
			if ann, ok := cmtAnns[ts.Doc]; ok {
				innerStack = append(innerStack, ann)
			}

			if len(innerStack) == 0 {
				continue
			}

			if !strings.HasSuffix(ts.Name.Name, *flagHandlerSuffix) || len(ts.Name.Name) == len(*flagHandlerSuffix) {
				par.Errors = append(par.Errors, fmt.Errorf(`%s: Handler type "%s" name must have suffix "%s" (and be longer than that)`,
					par.Pkg.Fset.Position(ts.Name.NamePos), ts.Name.Name, *flagHandlerSuffix))
			}

			curAnn := innerStack[len(innerStack)-1]

			if ev, err := par.ExtractEvent(curAnn.Decl, ts); err != nil {
				par.Errors = append(par.Errors, err)
			} else {
				curAnn.Decl.Events = append(curAnn.Decl.Events, ev)
			}

			for _, ann := range innerStack {
				ann.Visited = true
			}
		}
	}

	for _, ann := range anns {
		if !ann.Visited {
			par.Errors = append(par.Errors, fmt.Errorf("%s: Evon annotations apply only to func or interface type declarations",
				par.Pkg.Fset.Position(ann.Decl.Ann.Pos)))
		} else if len(ann.Errors) > 0 {
			par.Errors = append(par.Errors, ann.Errors...)
		} else {
			par.Decls = append(par.Decls, ann.Decl)
		}
	}
}

func (par *parser) ExtractEvent(decl *declRec, ts *ast.TypeSpec) (*eventRec, error) {
	underPkg, underType := par.resolver.Resolve(par.Pkg, ts.Type)

	switch realType := underType.(type) {
	case *ast.FuncType:
		return &eventRec{Name: ts.Name, Funcs: []*funcRec{par.extractFunc(underPkg, "", realType)}}, nil
	case *ast.InterfaceType:
		if funcs, ok := par.extractInterface(underPkg, realType, map[string]bool{}); !ok {
			return nil, fmt.Errorf(`%s: Cannot resolve type "%s" due to compilation errors`,
				par.Pkg.Fset.Position(ts.Name.NamePos), ts.Name.Name)
		} else if len(funcs) == 0 {
			return nil, fmt.Errorf(`%s: Interface type "%s" has no usable methods`,
				par.Pkg.Fset.Position(ts.Name.NamePos), ts.Name.Name)
		} else {
			return &eventRec{Name: ts.Name, Funcs: funcs}, nil
		}
	case nil:
		return nil, fmt.Errorf(`%s: Cannot resolve type "%s" due to compilation errors`,
			par.Pkg.Fset.Position(ts.Name.NamePos), ts.Name.Name)
	default:
		return nil, fmt.Errorf("%s: Evon annotations apply only to func or interface type declarations",
			par.Pkg.Fset.Position(decl.Ann.Pos))
	}
}

func (par *parser) importRecord(pkg *types.Package) *importRec {
	res, ok := par.Imports[pkg.Path()]
	if !ok {
		res = &importRec{Path: pkg.Path(), Name: pkg.Name()}
		par.Imports[pkg.Path()] = res
	}
	return res
}

func (par *parser) extractFunc(pkg *packages.Package, name string, typ *ast.FuncType) *funcRec {
	res := &funcRec{
		Name:   name,
		Params: typ.Params.List,
	}

	for _, g := range res.Params {
		ast.Walk(&typeVisitor{Parser: par, Pkg: pkg}, g.Type)
	}

	if typ.Results != nil {
		res.Returns = typ.Results.List

		for _, g := range res.Returns {
			ast.Walk(&typeVisitor{Parser: par, Pkg: pkg}, g.Type)
		}
	}

	return res
}

func (par *parser) extractInterface(pkg *packages.Package, typ *ast.InterfaceType, mthdNames map[string]bool) ([]*funcRec, bool) {
	res := []*funcRec{}

	for _, m := range typ.Methods.List {
		if len(m.Names) > 0 {
			if m.Names[0].IsExported() || pkg == par.Pkg {
				frec := par.extractFunc(pkg, m.Names[0].Name, m.Type.(*ast.FuncType))
				if !mthdNames[frec.Name] {
					mthdNames[frec.Name] = true
					res = append(res, frec)
				}
			}
			continue
		}

		embPkg, embType := par.resolver.Resolve(pkg, m.Type)
		embIntf, ok := embType.(*ast.InterfaceType)
		if !ok {
			return nil, false
		}
		embRes, ok := par.extractInterface(embPkg, embIntf, mthdNames)
		if !ok {
			return nil, false
		}
		res = append(res, embRes...)
	}

	return res, true
}

type typeVisitor struct {
	Parser  *parser
	Pkg     *packages.Package
	LastSel *ast.Ident
}

func (vis *typeVisitor) Visit(node ast.Node) ast.Visitor {
	switch realNode := node.(type) {
	case *ast.Ident:
		if obj, ok := vis.Pkg.TypesInfo.Uses[realNode]; ok {
			switch realObj := obj.(type) {
			case *types.PkgName:
				rec := vis.Parser.importRecord(realObj.Imported())
				rec.PkgIdents = append(rec.PkgIdents, realNode)
			case *types.TypeName:
				if realObj.Pkg() != nil && realObj.Pkg() != vis.Parser.Pkg.Types && realNode != vis.LastSel {
					rec := vis.Parser.importRecord(realObj.Pkg())
					rec.TypeIdents = append(rec.TypeIdents, realNode)
				}
			}
		}
	case *ast.SelectorExpr:
		vis.LastSel = realNode.Sel
	}

	return vis
}

func (par *parser) DedupImports() {
	if par.NeedSync && par.Imports["sync"] == nil {
		par.Imports["sync"] = &importRec{Path: "sync", Name: "sync"}
	}

	for _, r := range par.Imports {
		par.ImportList = append(par.ImportList, r)
	}
	sort.Slice(par.ImportList, func(i, j int) bool {
		iDot := strings.Contains(par.ImportList[i].Path, ".")
		jDot := strings.Contains(par.ImportList[j].Path, ".")
		return !iDot && jDot || iDot == jDot && par.ImportList[i].Path < par.ImportList[j].Path
	})

	par.PkgNameSet = newDedupSet()
	for _, r := range par.ImportList {
		r.Alias = par.PkgNameSet.Resolve(r.Name)

		for _, id := range r.TypeIdents {
			id.Name = r.Alias + "." + id.Name
		}
		for _, id := range r.PkgIdents {
			id.Name = r.Alias
		}
	}
}
