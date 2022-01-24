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
	"strings"

	"golang.org/x/tools/go/packages"
)

type parser struct {
	Pkg *packages.Package

	Decls   []*declRec
	Imports map[string]*importRec
	Errors  []error

	resolver typeResolver
}

type declRec struct {
	Ann   *annotation
	Event *eventRec
}

type eventRec struct {
	Name  *ast.Ident
	Funcs []*funcRec
}

type funcRec struct {
	Name string
	Type *ast.FuncType
}

type importRec struct {
	Path       string
	Name       string
	Alias      string
	PkgIdents  map[*ast.Ident]void
	TypeIdents map[*ast.Ident]void
	Priority   int
	Local      bool
}

const (
	prioStd = iota
	prioUser
	prioInternal
)

func newParser(pkg *packages.Package) *parser {
	return &parser{
		Pkg:      pkg,
		Imports:  make(map[string]*importRec),
		resolver: make(typeResolver),
	}
}

func (par *parser) ParsePkg() {
	for _, f := range par.Pkg.Syntax {
		par.ParseFile(f)
	}
}

func (par *parser) ParseFile(file *ast.File) {
	cgIdx := 0
	scanCmt := func(cur *ast.CommentGroup) *annotation {
		if cur == nil {
			return nil
		}

		for cgIdx < len(file.Comments) {
			cg := file.Comments[cgIdx]
			cgIdx++

			ann, err := extractAnnotation(cg, par.Pkg.Fset)
			if err != nil {
				par.Errors = append(par.Errors, err)
			} else if ann != nil && cg != cur {
				par.Errors = append(par.Errors, fmt.Errorf("%s: Evon annotations apply only to func or interface type declarations",
					par.Pkg.Fset.Position(ann.Pos)))
			}

			if cg == cur {
				return ann
			}
		}
		return nil
	}

	for _, d := range file.Decls {
		gd, ok := d.(*ast.GenDecl)
		if !ok || gd.Tok != token.TYPE {
			continue
		}

		grpAnn := scanCmt(gd.Doc)
		for _, s := range gd.Specs {
			ts := s.(*ast.TypeSpec)

			ann := scanCmt(ts.Doc)
			if ann == nil {
				ann = grpAnn
			}
			if ann == nil {
				continue
			}

			if !strings.HasSuffix(ts.Name.Name, *flagHandlerSuffix) || len(ts.Name.Name) == len(*flagHandlerSuffix) {
				par.Errors = append(par.Errors, fmt.Errorf(`%s: Handler type "%s" name must have suffix "%s" (and be longer than that)`,
					par.Pkg.Fset.Position(ts.Name.NamePos), ts.Name.Name, *flagHandlerSuffix))
			}

			if ev, err := par.ExtractEvent(ann, ts); err != nil {
				par.Errors = append(par.Errors, err)
			} else {
				par.Decls = append(par.Decls, &declRec{Ann: ann, Event: ev})

				if ann.Flags[annLock] || ann.Flags[annWait] {
					par.importRecord("sync", "sync", prioInternal)
				}
				if ann.Flags[annWait] {
					par.Imports["sync"].Local = true
				}
			}
		}
	}
}

func (par *parser) ExtractEvent(ann *annotation, ts *ast.TypeSpec) (*eventRec, error) {
	switch underPkg, underType := par.resolver.Resolve(par.Pkg, ts.Type); typeImpl := underType.(type) {
	case *ast.FuncType:
		return &eventRec{Name: ts.Name, Funcs: []*funcRec{par.extractFunc(underPkg, "", typeImpl)}}, nil
	case *ast.InterfaceType:
		if funcs, ok := par.extractInterface(underPkg, typeImpl, make(map[string]bool)); !ok {
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
			par.Pkg.Fset.Position(ann.Pos))
	}
}

func (par *parser) extractFunc(pkg *packages.Package, name string, typ *ast.FuncType) *funcRec {
	res := &funcRec{
		Name: name,
		Type: typ,
	}

	for _, g := range typ.Params.List {
		ast.Walk(&typeVisitor{Parser: par, Pkg: pkg}, g.Type)
	}

	if typ.Results != nil {
		for _, g := range typ.Results.List {
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

func (par *parser) importRecord(path, name string, prio int) *importRec {
	res, ok := par.Imports[path]
	if ok {
		if prio < res.Priority {
			res.Priority = prio
		}
	} else {
		if prio == prioUser && !strings.Contains(path, ".") {
			prio = prioStd
		}
		res = &importRec{
			Path:       path,
			Name:       name,
			Priority:   prio,
			PkgIdents:  make(map[*ast.Ident]void),
			TypeIdents: make(map[*ast.Ident]void),
		}
		par.Imports[path] = res
	}
	return res
}

type typeVisitor struct {
	Parser  *parser
	Pkg     *packages.Package
	LastSel *ast.Ident
}

func (vis *typeVisitor) Visit(node ast.Node) ast.Visitor {
	switch nodeImpl := node.(type) {
	case *ast.Ident:
		if obj, ok := vis.Pkg.TypesInfo.Uses[nodeImpl]; ok {
			switch objImpl := obj.(type) {
			case *types.PkgName:
				imp := objImpl.Imported()
				rec := vis.Parser.importRecord(imp.Path(), imp.Name(), prioUser)
				rec.PkgIdents[nodeImpl] = void{}
			case *types.TypeName:
				imp := objImpl.Pkg()
				if imp != nil && imp != vis.Parser.Pkg.Types && nodeImpl != vis.LastSel {
					rec := vis.Parser.importRecord(imp.Path(), imp.Name(), prioUser)
					rec.TypeIdents[nodeImpl] = void{}
				}
			}
		}
	case *ast.SelectorExpr:
		vis.LastSel = nodeImpl.Sel
	}

	return vis
}
