package main

import (
	"go/ast"
	"strconv"
)

const solanaImport = "github.com/solana-foundation/solana-go/v2"

var importRewrites = map[string]string{
	"github.com/gagliardetto/solana-go":             "github.com/solana-foundation/solana-go/v2",
	"github.com/gagliardetto/solana-go/rpc":         "github.com/solana-foundation/solana-go/v2/rpc",
	"github.com/gagliardetto/solana-go/text":        "github.com/solana-foundation/solana-go/v2/text",
	"github.com/gagliardetto/solana-go/text/format": "github.com/solana-foundation/solana-go/v2/text/format",
}

func rewriteImports(f *ast.File) {
	for _, imp := range f.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			continue
		}

		if to, ok := importRewrites[path]; ok {
			imp.Path.Value = strconv.Quote(to)
		}
	}
}

// rewriteSolanaAlias drops the solanago alias; the package name is solana.
func rewriteSolanaAlias(f *ast.File) {
	for _, imp := range f.Imports {
		if path, err := strconv.Unquote(imp.Path.Value); err == nil && path == solanaImport && imp.Name != nil && imp.Name.Name == "solanago" {
			imp.Name = nil
		}
	}

	ast.Inspect(f, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok && id.Name == "solanago" {
			id.Name = "solana"
		}

		return true
	})
}
