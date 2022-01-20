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
	"go/ast"
	"go/types"

	"golang.org/x/tools/go/packages"
)

type typeResolver map[*packages.Package]map[types.Object]*ast.Ident

func (reso typeResolver) Resolve(pkg *packages.Package, typ ast.Expr) (*packages.Package, ast.Expr) {
	switch typImpl := typ.(type) {
	case *ast.Ident:
		if typImpl.Obj != nil {
			return reso.Resolve(pkg, typImpl.Obj.Decl.(*ast.TypeSpec).Type)
		}

		target, ok := pkg.TypesInfo.Uses[typImpl]
		if !ok {
			return nil, nil
		}
		depPkg := pkg.Imports[target.Pkg().Path()]
		depIdent, ok := reso.identMap(depPkg)[target]
		if !ok {
			return nil, nil
		}

		return reso.Resolve(depPkg, depIdent)
	case *ast.SelectorExpr:
		return reso.Resolve(pkg, typImpl.Sel)
	case *ast.ParenExpr:
		return reso.Resolve(pkg, typImpl.X)
	default:
		return pkg, typ
	}
}

func (reso typeResolver) identMap(pkg *packages.Package) map[types.Object]*ast.Ident {
	res, ok := reso[pkg]
	if ok {
		return res
	}

	res = map[types.Object]*ast.Ident{}
	for i, o := range pkg.TypesInfo.Defs {
		if o != nil && o.Parent() == pkg.Types.Scope() && o.Exported() {
			res[o] = i
		}
	}
	reso[pkg] = res
	return res
}
