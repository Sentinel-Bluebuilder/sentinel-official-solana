package main

import (
	"go/ast"
	"strings"
)

func rewriteDiscriminators(f *ast.File) {
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv != nil || fn.Body == nil {
			continue
		}

		name := fn.Name.Name
		if !strings.HasPrefix(name, "new") || !strings.HasSuffix(name, "Instruction") {
			continue
		}

		ix := strings.TrimSuffix(strings.TrimPrefix(name, "new"), "Instruction")
		if ix == "" {
			continue
		}

		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) != 3 {
				return true
			}

			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "NewInstruction" {
				return true
			}

			id, ok := call.Args[2].(*ast.Ident)
			if !ok || id.Name != "nil" {
				return true
			}

			x := &ast.Ident{
				NamePos: id.NamePos,
				Name:    ix + "InstructionDiscriminator",
			}
			call.Args[2] = &ast.SliceExpr{
				X:      x,
				Lbrack: id.End(),
				Rbrack: id.End(),
			}

			return true
		})
	}
}
