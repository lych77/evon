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

	Decls  []*decl
	Errors []error

	IdentMaps map[*packages.Package]map[types.Object]*ast.Ident
	Imports   map[string]*importRec
	NeedSync  bool
}

type decl struct {
	Ann    *annotation
	Types  []*ast.TypeSpec
	Events []*eventRec
}

type eventRec struct {
	Name  *ast.Ident
	Funcs []*signature
}

type signature struct {
	Name    string
	Params  []*ast.Field
	Returns []*ast.Field
}

type importRec struct {
	Name       string
	Alias      string
	PkgIdents  []*ast.Ident
	TypeIdents []*ast.Ident
}

func newParser(pkg *packages.Package) *parser {
	return &parser{
		Pkg:       pkg,
		IdentMaps: map[*packages.Package]map[types.Object]*ast.Ident{},
		Imports:   map[string]*importRec{},
	}
}

func (par *parser) ParsePkg() {
	for _, f := range par.Pkg.Syntax {
		par.ParseFile(f)
	}

	par.ExtractTypes()
	if len(par.Errors) > 0 {
		return
	}

	par.DedupImports()
}

func (par *parser) ParseFile(file *ast.File) {
	type annRec struct {
		Decl    *decl
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
			rec := &annRec{Decl: &decl{Ann: ann}}
			anns = append(anns, rec)
			cmtAnns[cg] = rec

			if ann.Flags[annLock] || ann.Flags[annWait] {
				par.NeedSync = true
			}
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
			curAnn.Decl.Types = append(curAnn.Decl.Types, ts)

			for _, ann := range innerStack {
				ann.Visited = true
			}
		}
	}

	for _, ann := range anns {
		if !ann.Visited {
			par.Errors = append(par.Errors, fmt.Errorf("%s: Evon annotations apply only to func or interface type declarations",
				par.Pkg.Fset.Position(ann.Decl.Ann.Pos)))
			continue
		}

		if len(ann.Errors) > 0 {
			for _, e := range ann.Errors {
				par.Errors = append(par.Errors, e)
			}
			continue
		}

		par.Decls = append(par.Decls, ann.Decl)
	}
}

func (par *parser) ExtractTypes() {
	for _, decl := range par.Decls {
		for _, ts := range decl.Types {
			underType, underPkg := par.resolveType(par.Pkg, ts.Type)

			switch realType := underType.(type) {
			case *ast.FuncType:
				decl.Events = append(decl.Events, &eventRec{
					Name:  ts.Name,
					Funcs: []*signature{par.extractFunc(underPkg, "", realType)},
				})
			case *ast.InterfaceType:
				if funcs, ok := par.extractInterface(underPkg, realType); !ok {
					par.Errors = append(par.Errors, fmt.Errorf(`%s: Cannot resolve type "%s" due to compilation errors`,
						par.Pkg.Fset.Position(ts.Name.NamePos), ts.Name.Name))
				} else if len(funcs) == 0 {
					par.Errors = append(par.Errors, fmt.Errorf(`%s: Interface type "%s" has no usable methods`,
						par.Pkg.Fset.Position(ts.Name.NamePos), ts.Name.Name))
				} else {
					decl.Events = append(decl.Events, &eventRec{
						Name:  ts.Name,
						Funcs: funcs,
					})
				}
			case nil:
				par.Errors = append(par.Errors, fmt.Errorf(`%s: Cannot resolve type "%s" due to compilation errors`,
					par.Pkg.Fset.Position(ts.Name.NamePos), ts.Name.Name))
			default:
				par.Errors = append(par.Errors, fmt.Errorf("%s: Evon annotations apply only to func or interface type declarations",
					par.Pkg.Fset.Position(decl.Ann.Pos)))
			}
		}
	}
}

func (par *parser) identMap(pkg *packages.Package) map[types.Object]*ast.Ident {
	res, ok := par.IdentMaps[pkg]
	if ok {
		return res
	}

	res = map[types.Object]*ast.Ident{}
	for i, o := range pkg.TypesInfo.Defs {
		if o != nil && o.Parent() == pkg.Types.Scope() && o.Exported() {
			res[o] = i
		}
	}
	par.IdentMaps[pkg] = res
	return res
}

func (par *parser) importRecord(pkg *types.Package) *importRec {
	res, ok := par.Imports[pkg.Path()]
	if !ok {
		res = &importRec{Name: pkg.Name()}
		par.Imports[pkg.Path()] = res
	}
	return res
}

func (par *parser) resolveType(pkg *packages.Package, typ ast.Expr) (ast.Expr, *packages.Package) {
	switch realType := typ.(type) {
	case *ast.Ident:
		if realType.Obj != nil {
			return par.resolveType(pkg, realType.Obj.Decl.(*ast.TypeSpec).Type)
		}

		target, ok := pkg.TypesInfo.Uses[realType]
		if !ok {
			return nil, nil
		}
		depPkg := pkg.Imports[target.Pkg().Path()]
		depIdent, ok := par.identMap(depPkg)[target]
		if !ok {
			return nil, nil
		}

		return par.resolveType(depPkg, depIdent)
	case *ast.SelectorExpr:
		return par.resolveType(pkg, realType.Sel)
	case *ast.ParenExpr:
		return par.resolveType(pkg, realType.X)
	default:
		return typ, pkg
	}
}

func (par *parser) extractFunc(pkg *packages.Package, name string, typ *ast.FuncType) *signature {
	res := &signature{
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

func (par *parser) extractInterface(pkg *packages.Package, typ *ast.InterfaceType) ([]*signature, bool) {
	res := []*signature{}

	for _, m := range typ.Methods.List {
		if len(m.Names) > 0 {
			if m.Names[0].IsExported() || pkg == par.Pkg {
				frec := par.extractFunc(pkg, m.Names[0].Name, m.Type.(*ast.FuncType))
				res = append(res, frec)
			}
			continue
		}

		embType, embPkg := par.resolveType(pkg, m.Type)
		if embType == nil {
			return nil, false
		}
		embIntf, ok := embType.(*ast.InterfaceType)
		if !ok {
			return nil, false
		}
		embRes, ok := par.extractInterface(embPkg, embIntf)
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
	type pathRec struct {
		Path string
		Rec  *importRec
	}
	pathRecs := []*pathRec{}

	for p, r := range par.Imports {
		pathRecs = append(pathRecs, &pathRec{Path: p, Rec: r})
	}

	sort.Slice(pathRecs, func(i, j int) bool {
		return !strings.Contains(pathRecs[i].Path, ".") && strings.Contains(pathRecs[j].Path, ".")
	})

	dedup := newDedupSet("sync")
	for _, r := range pathRecs {
		r.Rec.Alias = dedup.Add(r.Rec.Name)

		for _, id := range r.Rec.TypeIdents {
			id.Name = r.Rec.Alias + "." + id.Name
		}

		if r.Rec.Alias == r.Rec.Name {
			r.Rec.Alias = ""
		} else {
			for _, id := range r.Rec.PkgIdents {
				id.Name = r.Rec.Name
			}
		}
	}

	if par.NeedSync {
		if _, ok := par.Imports["sync"]; !ok {
			par.Imports["sync"] = &importRec{Name: "sync"}
		}
	}
}
