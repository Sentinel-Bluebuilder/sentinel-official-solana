package main

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"strconv"

	"github.com/gagliardetto/anchor-go/idl"
	"github.com/gagliardetto/anchor-go/idl/idltype"
)

type extraImport struct {
	alias string
	path  string
}

func addInstructionDecoder(p *program, files map[string][]byte) error {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "instructions.go", files["instructions.go"], 0)
	if err != nil {
		return err
	}

	params := map[string][]string{}
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		for _, field := range fn.Type.Params.List {
			var b bytes.Buffer
			if err := format.Node(&b, fset, field.Type); err != nil {
				return err
			}
			for range field.Names {
				params[fn.Name.Name] = append(params[fn.Name.Name], b.String())
			}
		}
	}

	var body bytes.Buffer
	body.WriteString("func DecodeInstruction(accounts []*solana.AccountMeta, data []byte) (any, error) {\n\tswitch {\n")
	for _, ix := range p.doc.Instructions {
		name := exportedName(ix.Name)
		types := params["new"+name+"Instruction"]
		if len(types) < len(ix.Args) {
			return fmt.Errorf("%s: upstream instruction arguments missing", ix.Name)
		}
		fmt.Fprintf(&body, "\tcase len(data) >= len(%sInstructionDiscriminator) && bytes.Equal(data[:len(%sInstructionDiscriminator)], %sInstructionDiscriminator[:]):\n", name, name, name)
		fmt.Fprintf(&body, "\t\tif len(accounts) < %d {\n\t\t\treturn nil, fmt.Errorf(%q, len(accounts))\n\t\t}\n", len(ix.Accounts), ix.Name+": expected at least "+strconv.Itoa(len(ix.Accounts))+" accounts, got %d")
		if len(ix.Args) > 0 {
			fmt.Fprintf(&body, "\t\tdec := binary.NewBorshDecoder(data[len(%sInstructionDiscriminator):])\n", name)
			for i, arg := range ix.Args {
				if err := writeInstructionArg(p, &body, arg.Ty, types[i], ix.Name+": "+arg.Name); err != nil {
					return err
				}
			}
			fmt.Fprintf(&body, "\t\tif dec.Remaining() != 0 {\n\t\t\treturn nil, fmt.Errorf(%q, dec.Remaining())\n\t\t}\n", ix.Name+": unexpected trailing instruction data: %d bytes")
		} else {
			fmt.Fprintf(&body, "\t\tif len(data) != len(%sInstructionDiscriminator) {\n\t\t\treturn nil, fmt.Errorf(%q)\n\t\t}\n", name, ix.Name+": unexpected trailing instruction data")
		}
		body.WriteString("\t\treturn solana.NewInstruction(ProgramID, solana.AccountMetaSlice(accounts), data), nil\n")
	}
	body.WriteString("\t}\n\treturn nil, fmt.Errorf(\"unknown instruction discriminator: %x\", data)\n}\n")
	instructionImports := []extraImport{{path: "fmt"}, {path: "github.com/gagliardetto/solana-go"}}
	if len(p.doc.Instructions) > 0 {
		instructionImports = append(instructionImports, extraImport{path: "bytes"})
	}
	files["instructions.go"], err = appendSource(files["instructions.go"], body.String(), instructionImports)
	if err != nil {
		return err
	}

	var program bytes.Buffer
	fmt.Fprintf(&program, "const ProgramName = %q\n\n", p.doc.Metadata.Name)
	program.WriteString(`func init() {
	solana.MustRegisterInstructionDecoder(ProgramID, DecodeInstruction)
}

func SetProgramID(programID solana.PublicKey) error {
	if err := solana.RegisterInstructionDecoder(programID, DecodeInstruction); err != nil {
		return err
	}
	ProgramID = programID
	return nil
}
`)
	files["program_id.go"], err = appendSource(files["program_id.go"], program.String(), nil)
	return err
}

func appendSource(src []byte, body string, imports []extraImport) ([]byte, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "upstream.go", src, parser.ParseComments)
	if err != nil {
		return nil, err
	}
	present := map[string]bool{}
	for _, imp := range f.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			return nil, err
		}
		present[path] = true
	}
	var specs []ast.Spec
	for _, imp := range imports {
		if present[imp.path] {
			continue
		}
		spec := &ast.ImportSpec{Path: &ast.BasicLit{
			Kind:  token.STRING,
			Value: strconv.Quote(imp.path),
		}}
		if imp.alias != "" {
			spec.Name = ast.NewIdent(imp.alias)
		}
		specs = append(specs, spec)
		present[imp.path] = true
	}
	if len(specs) > 0 {
		f.Decls = append([]ast.Decl{&ast.GenDecl{
			Tok:   token.IMPORT,
			Specs: specs,
		}}, f.Decls...)
	}
	added, err := parser.ParseFile(fset, "extra.go", "package "+f.Name.Name+"\n"+body, 0)
	if err != nil {
		return nil, err
	}
	f.Decls = append(f.Decls, added.Decls...)
	var b bytes.Buffer
	if err := format.Node(&b, fset, f); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func elementType(goType string) (string, error) {
	expr, err := parser.ParseExpr(goType)
	if err != nil {
		return "", err
	}
	var elem ast.Expr
	switch t := expr.(type) {
	case *ast.StarExpr:
		elem = t.X
	case *ast.ArrayType:
		elem = t.Elt
	default:
		return "", fmt.Errorf("expected an option, array or vector type, got %s", goType)
	}
	var b bytes.Buffer
	if err := format.Node(&b, token.NewFileSet(), elem); err != nil {
		return "", err
	}
	return b.String(), nil
}

func writeInstructionArg(p *program, w *bytes.Buffer, ty idltype.IdlType, goType, label string) error {
	w.WriteString("{\n")
	switch t := ty.(type) {
	case *idltype.Option, *idltype.COption:
		reader := "ReadOption"
		var inner idltype.IdlType
		if option, ok := t.(*idltype.Option); ok {
			inner = option.Option
		} else {
			reader = "ReadCOption"
			inner = t.(*idltype.COption).COption
		}
		fmt.Fprintf(w, "present, err := dec.%s()\nif err != nil { return nil, fmt.Errorf(%q, err) }\nif present {\n", reader, label+": %w")
		elem, err := elementType(goType)
		if err != nil {
			return err
		}
		if err := writeInstructionArg(p, w, inner, elem, label); err != nil {
			return err
		}
		w.WriteString("}\n")
	case *idltype.Vec:
		fmt.Fprintf(w, "length, err := dec.ReadLength()\nif err != nil { return nil, fmt.Errorf(%q, err) }\nfor i := 0; i < length; i++ {\n", label+": %w")
		elem, err := elementType(goType)
		if err != nil {
			return err
		}
		if err := writeInstructionArg(p, w, t.Vec, elem, label); err != nil {
			return err
		}
		w.WriteString("}\n")
	case *idltype.Array:
		n, ok := t.Size.(*idltype.IdlArrayLenValue)
		if !ok {
			return fmt.Errorf("%s: generic array length is not supported", label)
		}
		fmt.Fprintf(w, "for i := 0; i < %d; i++ {\n", n.Value)
		elem, err := elementType(goType)
		if err != nil {
			return err
		}
		if err := writeInstructionArg(p, w, t.Type, elem, label); err != nil {
			return err
		}
		w.WriteString("}\n")
	default:
		if defined, ok := ty.(*idltype.Defined); ok {
			td := p.doc.Types.ByName(defined.Name)
			if td == nil {
				return fmt.Errorf("%s: type %s not found", label, defined.Name)
			}
			if enum, ok := td.Ty.(*idl.IdlTypeDefTyEnum); ok && !enum.IsAllSimple() {
				fmt.Fprintf(w, "if _, err := Decode%s(dec); err != nil { return nil, fmt.Errorf(%q, err) }\n", exportedName(defined.Name), label+": %w")
				break
			}
		}
		fmt.Fprintf(w, "var value %s\nif err := dec.Decode(&value); err != nil { return nil, fmt.Errorf(%q, err) }\n", goType, label+": %w")
	}
	w.WriteString("}\n")
	return nil
}
