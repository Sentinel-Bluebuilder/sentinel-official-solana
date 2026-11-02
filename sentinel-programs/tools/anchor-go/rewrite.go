package main

import (
	"bytes"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
)

var fileRewrites = []rewrite{rewriteImports, rewriteSolanaAlias, rewriteHelperCalls, rewriteDiscriminators}

type rewrite func(*ast.File)

func applyRewrites(src []byte, rewrites ...rewrite) ([]byte, error) {
	fset := token.NewFileSet()

	f, err := parser.ParseFile(fset, "", src, parser.ParseComments)
	if err != nil {
		return nil, err
	}

	for _, r := range rewrites {
		r(f)
	}

	var b bytes.Buffer
	if err := format.Node(&b, fset, f); err != nil {
		return nil, err
	}

	return b.Bytes(), nil
}
