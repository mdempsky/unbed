// Copyright 2017 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"flag"
	"fmt"
	"go/ast"
	"go/format"
	"go/token"
	"go/types"
	"io/ioutil"
	"log"
	"os"

	"golang.org/x/tools/go/ast/astutil"
	"golang.org/x/tools/go/packages"
)

var (
	fset = token.NewFileSet()

	pkgPath, typeName, fieldName string

	owner *types.Struct
	field *types.Var
)

func main() {
	flag.Parse()

	var err error
	pkgPath, typeName, fieldName, err = parseFieldSpec(flag.Arg(0))
	if err != nil {
		log.Fatal(err)
	}

	// TODO(mdempsky): Restore importgraph logic for finding dependent packages.

	conf := packages.Config{
		Mode: packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo,
		Fset: fset,
	}
	pkgs, err := packages.Load(&conf, pkgPath)
	if err != nil {
		log.Fatal(err)
	}

	ownerPkg := pkgs[0].Types
	owner = ownerPkg.Scope().Lookup(typeName).(*types.TypeName).Type().Underlying().(*types.Struct)
	obj, index, _ := types.LookupFieldOrMethod(owner, false, ownerPkg, fieldName)
	if v, ok := obj.(*types.Var); !ok || !v.IsField() || !v.Anonymous() || len(index) != 1 {
		log.Fatal("expected immediate embedded field name")
	}
	field = obj.(*types.Var)

	totalCount := 0
	totalFiles := 0
	totalPackages := 0

	for _, pkg := range pkgs {
		pkgMatch := false
		for _, file := range pkg.Syntax {
			u := unbedder{pkg: pkg}
			ast.Walk(&u, file)
			if len(u.res) != 0 {
				totalCount += len(u.res)
				totalFiles++
				pkgMatch = true

				edit(fset.File(file.Pos()), u.res)
			}
		}
		if pkgMatch {
			totalPackages++
		}
	}

	fmt.Fprintf(os.Stderr, "Rewrote %d selections in %d files in %d packages.\n", totalCount, totalFiles, totalPackages)
}

func edit(f *token.File, pos []token.Pos) {
	buf, err := ioutil.ReadFile(f.Name())
	if err != nil {
		log.Fatal(err)
	}
	s := string(buf)
	for i := len(pos) - 1; i >= 0; i-- {
		o := f.Position(pos[i]).Offset
		s = s[:o] + fieldName + "." + s[o:]
	}
	buf = []byte(s)
	buf, err = format.Source(buf)
	if err != nil {
		log.Fatal(err)
	}
	err = ioutil.WriteFile(f.Name(), buf, 0o666)
	if err != nil {
		log.Fatal(err)
	}
}

type unbedder struct {
	pkg  *packages.Package
	path []ast.Node
	res  []token.Pos
}

func (e *unbedder) Visit(n ast.Node) ast.Visitor {
	if se, ok := n.(*ast.SelectorExpr); ok {
		e.selector(se)
	}

	if n != nil {
		e.path = append(e.path, n)
	} else {
		e.path = e.path[:len(e.path)-1]
	}

	return e
}

func (e *unbedder) selector(se *ast.SelectorExpr) {
	sel, ok := e.pkg.TypesInfo.Selections[se]
	if !ok {
		// Qualified identifier.
		return
	}
	idx := sel.Index()
	if len(idx) == 1 {
		// Direct field/method access.
		return
	}

	tv := e.pkg.TypesInfo.Types[se.X]
	typ := tv.Type
	for _, fi := range idx[:len(idx)-1] {
		if ptr, ok := typ.Underlying().(*types.Pointer); ok {
			typ = ptr.Elem()
		}
		f := typ.Underlying().(*types.Struct).Field(fi)
		if f != field {
			typ = f.Type()
			continue
		}

		pos := se.Sel.Pos()

		// Issue #4: don't rewrite method expression T.M to T.U.M.
		if tv.IsType() {
			fmt.Fprintf(os.Stderr, "%s: implicit field traversal in method expression\n", fset.Position(pos))
			return
		}

		// Issue #2: don't rewrite unsafe.Offsetof(x.f) to unsafe.Offsetof(x.e.f).
		if call, ok := e.path[len(e.path)-1].(*ast.CallExpr); ok && e.isUnsafeOffsetof(call.Fun) {
			fmt.Fprintf(os.Stderr, "%s: implicit field traversal in unsafe.Offsetof call\n", fset.Position(pos))
			return
		}

		// Issue #1: don't rewrite x.f to x.e.f if they don't select the same field.
		if obj, _, _ := types.LookupFieldOrMethod(tv.Type, tv.Addressable(), e.pkg.Types, fieldName); obj != field {
			fmt.Fprintf(os.Stderr, "%s: failed to rewrite implicit field traversal\n", fset.Position(pos))
			return
		}

		e.res = append(e.res, pos)
		return
	}
}

func (e *unbedder) isUnsafeOffsetof(fun ast.Expr) bool {
	var ident *ast.Ident
	switch fun := astutil.Unparen(fun).(type) {
	case *ast.Ident:
		ident = fun
	case *ast.SelectorExpr:
		ident = fun.Sel
	default:
		return false
	}

	b, ok := e.pkg.TypesInfo.Uses[ident].(*types.Builtin)
	return ok && b.Name() == "Offsetof"
}
