package main

import (
	"go/ast"
	"go/token"
	"path"
	"strconv"
)

const helperImport = "github.com/gagliardetto/anchor-go/errors"

var helperCalls = map[string]string{
	"NewField":  "newFieldError",
	"NewIndex":  "newIndexError",
	"NewOption": "newOptionError",
}

func removeImport(f *ast.File, importPath string) string {
	alias := ""
	decls := f.Decls[:0]

	for _, decl := range f.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.IMPORT {
			decls = append(decls, decl)

			continue
		}

		specs := gen.Specs[:0]

		for _, spec := range gen.Specs {
			imp := spec.(*ast.ImportSpec)

			p, err := strconv.Unquote(imp.Path.Value)
			if err == nil && p == importPath {
				alias = path.Base(p)
				if imp.Name != nil {
					alias = imp.Name.Name
				}

				continue
			}

			specs = append(specs, spec)
		}

		gen.Specs = specs
		if len(specs) > 0 {
			decls = append(decls, gen)
		}
	}

	f.Decls = decls

	imports := f.Imports[:0]

	for _, imp := range f.Imports {
		if p, err := strconv.Unquote(imp.Path.Value); err != nil || p != importPath {
			imports = append(imports, imp)
		}
	}

	f.Imports = imports

	return alias
}

func rewriteHelperCalls(f *ast.File) {
	alias := removeImport(f, helperImport)
	if alias == "" {
		return
	}

	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}

		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}

		pkg, ok := sel.X.(*ast.Ident)
		if !ok || pkg.Name != alias {
			return true
		}

		if local, ok := helperCalls[sel.Sel.Name]; ok {
			call.Fun = ast.NewIdent(local)
		}

		return true
	})
}
