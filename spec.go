// Copyright 2017 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
)

var errBadSpec = errors.New("bad spec expression")

func parseFieldSpec(expr string) (pkgPath, typeName, fieldName string, err error) {
	// Inspired by x/tools/refactor/rename/spec.go.

	e, err := parser.ParseExpr(expr)
	if err != nil {
		return "", "", "", err
	}

	x, ok := e.(*ast.SelectorExpr)
	if !ok {
		return "", "", "", errBadSpec
	}
	fieldName = x.Sel.Name

	x, ok = x.X.(*ast.SelectorExpr)
	if !ok {
		return "", "", "", errBadSpec
	}
	typeName = x.Sel.Name

	switch x := x.X.(type) {
	case *ast.Ident:
		pkgPath = x.Name
	case *ast.BasicLit:
		if x.Kind == token.STRING {
			pkgPath, _ = strconv.Unquote(x.Value)
		}
	default:
		return "", "", "", errBadSpec
	}

	return
}
