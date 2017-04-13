// Copyright 2017 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"flag"
	"fmt"
	"go/ast"
	"go/build"
	"go/format"
	"go/token"
	"go/types"
	"io/ioutil"
	"log"
	"os"

	"golang.org/x/tools/go/loader"
	"golang.org/x/tools/refactor/importgraph"
)

var (
	fset = token.NewFileSet()

	pkgPath, typeName, fieldName string

	owner      *types.Struct
	fieldIndex int = -1
)

func main() {
	flag.Parse()

	var err error
	pkgPath, typeName, fieldName, err = parseFieldSpec(flag.Arg(0))
	if err != nil {
		log.Fatal(err)
	}

	ctxt := &build.Default
	conf := loader.Config{
		Build: ctxt,
		Fset:  fset,
	}

	_, reverse, errors := importgraph.Build(ctxt)
	if len(errors) != 0 {
		log.Fatal(errors)
	}

	for path := range reverse.Search(pkgPath) {
		conf.ImportWithTests(path)
	}

	prog, err := conf.Load()
	if err != nil {
		log.Fatal(err)
	}

	ownerPkg := prog.Package(pkgPath).Pkg
	owner = ownerPkg.Scope().Lookup(typeName).(*types.TypeName).Type().Underlying().(*types.Struct)
	obj, index, _ := types.LookupFieldOrMethod(owner, false, ownerPkg, fieldName)
	if v, ok := obj.(*types.Var); !ok || !v.IsField() || len(index) != 1 {
		log.Fatal("expected immediate field name")
	}
	fieldIndex = index[0]

	for _, info := range prog.InitialPackages() {
		for _, file := range info.Files {
			var u unbedder
			ast.Inspect(file, func(n ast.Node) bool {
				if se, ok := n.(*ast.SelectorExpr); ok {
					u.do(info.Pkg, &info.Info, se)
				}
				return true
			})
			if len(u.res) != 0 {
				edit(fset.File(file.Pos()), u.res)
			}
		}
	}
}

func edit(f *token.File, pos []token.Pos) {
	filename := f.Name()
	fmt.Fprintf(os.Stderr, "=== %s (%d matches)\n", filename, len(pos))

	buf, err := ioutil.ReadFile(filename)
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
	err = ioutil.WriteFile(filename, buf, 0666)
	if err != nil {
		log.Fatal(err)
	}
}

type unbedder struct {
	res []token.Pos
}

func (e *unbedder) do(pkg *types.Package, info *types.Info, se *ast.SelectorExpr) {
	sel, ok := info.Selections[se]
	if !ok {
		// Qualified identifier.
		return
	}
	idx := sel.Index()
	if len(idx) == 1 {
		// Direct field/method access.
		return
	}
	typ := info.Types[se.X].Type
	for _, i := range idx[:len(idx)-1] {
		if ptr, ok := typ.Underlying().(*types.Pointer); ok {
			typ = ptr.Elem()
		}
		str := typ.Underlying().(*types.Struct)
		if str == owner && i == fieldIndex {
			e.res = append(e.res, se.Sel.Pos())
			// TODO(mdempsky): I'm pretty sure there can
			// only be one, but prove it.
			return
		}
		typ = str.Field(i).Type()
	}
}

type posByPos []token.Pos

func (s posByPos) Len() int           { return len(s) }
func (s posByPos) Swap(i, j int)      { s[i], s[j] = s[j], s[i] }
func (s posByPos) Less(i, j int) bool { return s[i] < s[j] }
