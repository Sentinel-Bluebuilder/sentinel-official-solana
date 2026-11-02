package main

import (
	"fmt"
	"go/token"
	"go/types"
	"strings"

	"github.com/gagliardetto/anchor-go/idl"
	"github.com/gagliardetto/anchor-go/idl/idltype"
	"github.com/gagliardetto/anchor-go/tools"
)

type flagBinding struct {
	condition string
	name      string
	required  bool
	stmt      string
}

type flagPlan struct {
	bindings       []flagBinding
	declarations   []string
	postStatements []string
	reserved       map[string]bool
}

func (fs *flagPlan) add(name, stmt string, required bool) {
	fs.bindings = append(fs.bindings, flagBinding{
		name:     name,
		stmt:     stmt,
		required: required,
	})
}

func (fs *flagPlan) allocateLocal(base string) string {
	if fs.reserved == nil {
		fs.reserved = make(map[string]bool)

		for _, name := range types.Universe.Names() {
			fs.reserved[name] = true
		}

		for _, name := range []string{"r", "p", "cmd", "ctx", "signer", "ix", "err", "k", "ok", "context", "fmt", "cli", "solana", "rpc", "cobra", "bin"} {
			fs.reserved[name] = true
		}
	}

	if base == "_" || !token.IsIdentifier(base) {
		base = "value"
	}

	name := base
	for suffix := 2; fs.reserved[name]; suffix++ {
		name = fmt.Sprintf("%s%d", base, suffix)
	}

	fs.reserved[name] = true

	return name
}

func (fs *flagPlan) applyCondition(bindings, post int, condition string) {
	for i := bindings; i < len(fs.bindings); i++ {
		if fs.bindings[i].condition == "" {
			fs.bindings[i].condition = condition
		} else {
			fs.bindings[i].condition = "(" + condition + ") && (" + fs.bindings[i].condition + ")"
		}
	}

	for i := post; i < len(fs.postStatements); i++ {
		fs.postStatements[i] = fmt.Sprintf("if %s {\n%s\n}", condition, fs.postStatements[i])
	}
}

// fieldFlag names the flag of a struct field. The IDL wrapper argument "args"
// carries no meaning of its own, so its fields are named without the prefix.
func fieldFlag(parent, field string) string {
	if parent == "args" {
		return toKebab(field)
	}

	return parent + "." + toKebab(field)
}

func isRentRecipientArg(name string, t idltype.IdlType) bool {
	_, ok := t.(*idltype.Pubkey)

	return ok && strings.HasSuffix(name, "_rent_recipient")
}

func jsonParser(p *program, t idltype.IdlType) (string, error) {
	qt, err := goType(p.doc, t)
	if err != nil {
		return "", err
	}

	var enums []string
	seen := make(map[string]bool)
	var visit func(idltype.IdlType) error
	fields := func(value idl.IdlDefinedFields) error {
		switch value := value.(type) {
		case idl.IdlDefinedFieldsNamed:
			for _, field := range value {
				if err := visit(field.Ty); err != nil {
					return err
				}
			}
		case idl.IdlDefinedFieldsTuple:
			for _, field := range value {
				if err := visit(field); err != nil {
					return err
				}
			}
		}

		return nil
	}

	visit = func(t idltype.IdlType) error {
		switch value := t.(type) {
		case *idltype.Array:
			return visit(value.Type)
		case *idltype.Vec:
			return visit(value.Vec)
		case *idltype.Option:
			return visit(value.Option)
		case *idltype.COption:
			return visit(value.COption)
		case *idltype.Defined:
			if seen[value.Name] {
				return nil
			}

			seen[value.Name] = true
			td := p.doc.Types.ByName(value.Name)
			if td == nil {
				return fmt.Errorf("type %s not found", value.Name)
			}

			switch ty := td.Ty.(type) {
			case *idl.IdlTypeDefTyStruct:
				return fields(ty.Fields)
			case *idl.IdlTypeDefTyEnum:
				if ty.IsAllSimple() {
					return nil
				}

				names := make(map[string]bool)
				variants := make([]string, 0, len(ty.Variants))
				for _, variant := range ty.Variants {
					tag := toKebab(tools.ToSnake(variant.Name))
					if names[tag] {
						return fmt.Errorf("enum %s: duplicate JSON variant tag %q", value.Name, tag)
					}

					names[tag] = true
					variants = append(variants, fmt.Sprintf("%q: func() %s { return &%s%s{} }", tag, exportedName(value.Name), exportedName(value.Name), exportedName(variant.Name)))
					if variant.Fields.IsSome() {
						if err := fields(variant.Fields.Unwrap()); err != nil {
							return err
						}
					}
				}

				enums = append(enums, fmt.Sprintf("cli.NewEnumDecoder[%s](map[string]func() %s{%s})", exportedName(value.Name), exportedName(value.Name), strings.Join(variants, ", ")))
			}
		}

		return nil
	}

	if err := visit(t); err != nil {
		return "", err
	}

	if len(enums) == 0 {
		return "cli.ParseJSON[" + qt + "]", nil
	}

	return fmt.Sprintf("cli.ParseJSONWithEnums[%s](%s)", qt, strings.Join(enums, ", ")), nil
}

func localName(flag string) string {
	return unexportedName(strings.NewReplacer(".", "_", "-", "_").Replace(flag))
}

func nativeBinder(t idltype.IdlType) (string, string, bool) {
	switch t.(type) {
	case *idltype.Bool:
		return "BoolVar", "false", true
	case *idltype.U8:
		return "Uint8Var", "0", true
	case *idltype.U16:
		return "Uint16Var", "0", true
	case *idltype.U32:
		return "Uint32Var", "0", true
	case *idltype.U64:
		return "Uint64Var", "0", true
	case *idltype.I8:
		return "Int8Var", "0", true
	case *idltype.I16:
		return "Int16Var", "0", true
	case *idltype.I32:
		return "Int32Var", "0", true
	case *idltype.I64:
		return "Int64Var", "0", true
	case *idltype.F32:
		return "Float32Var", "0", true
	case *idltype.F64:
		return "Float64Var", "0", true
	case *idltype.String:
		return "StringVar", `""`, true
	}

	return "", "", false
}

// phrase turns an IDL name into the lowercase words of a help text.
func phrase(name string) string {
	if name == strings.ToLower(name) {
		return strings.ReplaceAll(name, "_", " ")
	}

	return strings.ReplaceAll(tools.ToSnake(name), "_", " ")
}

func planEnum(p *program, name, target, typeName string, ty *idl.IdlTypeDefTyEnum, usage string, out *flagPlan) error {
	base := localName(name)
	sel := out.allocateLocal(base + "Sel")
	names := make([]string, 0, len(ty.Variants))

	for _, v := range ty.Variants {
		names = append(names, toKebab(tools.ToSnake(v.Name)))
	}

	out.declarations = append(out.declarations, fmt.Sprintf("var %s string", sel))
	out.add(name, fmt.Sprintf("cmd.Flags().StringVar(&%s, %q, \"\", %q)", sel, name, usage+": "+strings.Join(names, ", ")), true)
	cases := make([]string, 0, len(ty.Variants))

	for i, v := range ty.Variants {
		if ty.IsAllSimple() {
			cases = append(cases, fmt.Sprintf("case %q:\n%s = %s%s", names[i], target, exportedName(typeName), exportedName(v.Name)))

			continue
		}

		local := out.allocateLocal(base + exportedName(v.Name))
		out.declarations = append(out.declarations, fmt.Sprintf("var %s %s%s", local, exportedName(typeName), exportedName(v.Name)))

		if v.Fields.IsSome() {
			fields, ok := v.Fields.Unwrap().(idl.IdlDefinedFieldsNamed)
			if !ok {
				return fmt.Errorf("enum %s: tuple variant %s is not supported", typeName, v.Name)
			}

			bindings, post := len(out.bindings), len(out.postStatements)

			for _, f := range fields {
				fieldName := name + "." + names[i] + "." + toKebab(f.Name)
				fieldTarget := local + "." + exportedName(f.Name)

				if err := planFlags(p, fieldName, fieldTarget, f.Ty, phrase(f.Name), out); err != nil {
					return err
				}
			}

			out.applyCondition(bindings, post, fmt.Sprintf("%s == %q", sel, names[i]))
		}

		cases = append(cases, fmt.Sprintf("case %q:\n%s = &%s", names[i], target, local))
	}

	errFormat := "--" + name + ": expected one of " + strings.Join(names, ", ") + ", got %q"
	out.postStatements = append(out.postStatements, fmt.Sprintf("if cmd.Flags().Changed(%q) {\nswitch %s {\n%s\ndefault:\nreturn fmt.Errorf(%q, %s)\n}\n}", name, sel, strings.Join(cases, "\n"), errFormat, sel))

	return nil
}

func planFlags(p *program, name, target string, t idltype.IdlType, usage string, out *flagPlan) error {
	switch v := t.(type) {
	case *idltype.Option:
		return planOptional(p, name, target, v.Option, usage, out)
	case *idltype.COption:
		return planOptional(p, name, target, v.COption, usage, out)
	case *idltype.Vec:
		if typ, parser, ok := scalarParser(v.Vec); ok {
			out.add(name, fmt.Sprintf("cli.SliceVar(cmd.Flags(), &%s, %q, %q, %q, %s)", target, name, usage, typ, parser), true)

			return nil
		}

		return planJSON(p, name, target, t, usage, out)
	case *idltype.Array:
		if _, ok := v.Type.(*idltype.U8); ok {
			out.add(name, fmt.Sprintf("cli.FixedBytesVar(cmd.Flags(), %s[:], %q, %q)", target, name, usage), true)

			return nil
		}

		return planJSON(p, name, target, t, usage, out)
	case *idltype.Defined:
		td := p.doc.Types.ByName(v.Name)
		if td == nil {
			return fmt.Errorf("type %s not found", v.Name)
		}

		switch ty := td.Ty.(type) {
		case *idl.IdlTypeDefTyStruct:
			fields, ok := ty.Fields.(idl.IdlDefinedFieldsNamed)
			if !ok {
				return planJSON(p, name, target, t, usage, out)
			}

			for _, f := range fields {
				fieldTarget := target + "." + exportedName(f.Name)

				if err := planFlags(p, fieldFlag(name, f.Name), fieldTarget, f.Ty, phrase(f.Name), out); err != nil {
					return err
				}
			}

			return nil
		case *idl.IdlTypeDefTyEnum:
			return planEnum(p, name, target, v.Name, ty, usage, out)
		}
	}

	if method, zero, ok := nativeBinder(t); ok {
		out.add(name, fmt.Sprintf("cmd.Flags().%s(&%s, %q, %s, %q)", method, target, name, zero, usage), true)

		return nil
	}

	if typ, parser, ok := scalarParser(t); ok {
		out.add(name, fmt.Sprintf("cli.Var(cmd.Flags(), &%s, %q, %q, %q, %s)", target, name, usage, typ, parser), true)

		return nil
	}

	return planJSON(p, name, target, t, usage, out)
}

func planJSON(p *program, name, target string, t idltype.IdlType, usage string, out *flagPlan) error {
	parser, err := jsonParser(p, t)
	if err != nil {
		return err
	}

	out.add(name, fmt.Sprintf("cli.Var(cmd.Flags(), &%s, %q, %q, \"json\", %s)", target, name, usage, parser), true)

	return nil
}

func planOptional(p *program, name, target string, inner idltype.IdlType, usage string, out *flagPlan) error {
	if d, ok := inner.(*idltype.Defined); ok {
		td := p.doc.Types.ByName(d.Name)
		if td == nil {
			return fmt.Errorf("type %s not found", d.Name)
		}

		switch ty := td.Ty.(type) {
		case *idl.IdlTypeDefTyStruct:
			if fields, ok := ty.Fields.(idl.IdlDefinedFieldsNamed); ok {
				local := out.allocateLocal(localName(name))
				out.declarations = append(out.declarations, fmt.Sprintf("var %s %s", local, exportedName(d.Name)))
				bindings, post := len(out.bindings), len(out.postStatements)

				for _, f := range fields {
					fieldName := name + "." + toKebab(f.Name)
					fieldTarget := local + "." + exportedName(f.Name)

					if err := planFlags(p, fieldName, fieldTarget, f.Ty, phrase(f.Name), out); err != nil {
						return err
					}
				}

				out.postStatements = append(out.postStatements, fmt.Sprintf("%s = &%s", target, local))
				out.applyCondition(bindings, post, fmt.Sprintf("cli.AnyChangedWithPrefix(cmd.Flags(), %q)", name+"."))

				return nil
			}
		case *idl.IdlTypeDefTyEnum:
			if !ty.IsAllSimple() {
				return fmt.Errorf("option of enum %s is not supported", d.Name)
			}

			local := out.allocateLocal(localName(name))
			out.declarations = append(out.declarations, fmt.Sprintf("var %s %s", local, exportedName(d.Name)))
			bindings := len(out.bindings)

			if err := planEnum(p, name, local, d.Name, ty, usage, out); err != nil {
				return err
			}

			out.bindings[bindings].required = false
			out.postStatements = append(out.postStatements, fmt.Sprintf("if cmd.Flags().Changed(%q) {\n%s = &%s\n}", name, target, local))

			return nil
		}
	}

	if arr, ok := inner.(*idltype.Array); ok {
		if _, ok := arr.Type.(*idltype.U8); ok {
			qt, err := goType(p.doc, inner)
			if err != nil {
				return err
			}

			out.add(name, fmt.Sprintf("cli.OptionalVar(cmd.Flags(), &%s, %q, %q, \"bytes\", cli.ParseByteArray[%s])", target, name, usage, qt), false)

			return nil
		}
	}

	if typ, parser, ok := scalarParser(inner); ok {
		out.add(name, fmt.Sprintf("cli.OptionalVar(cmd.Flags(), &%s, %q, %q, %q, %s)", target, name, usage, typ, parser), false)

		return nil
	}

	parser, err := jsonParser(p, inner)
	if err != nil {
		return err
	}

	out.add(name, fmt.Sprintf("cli.OptionalVar(cmd.Flags(), &%s, %q, %q, \"json\", %s)", target, name, usage, parser), false)

	return nil
}

func scalarParser(t idltype.IdlType) (string, string, bool) {
	switch t.(type) {
	case *idltype.Bool:
		return "bool", "cli.ParseBool", true
	case *idltype.U8:
		return "uint8", "cli.ParseUint[uint8]", true
	case *idltype.U16:
		return "uint16", "cli.ParseUint[uint16]", true
	case *idltype.U32:
		return "uint32", "cli.ParseUint[uint32]", true
	case *idltype.U64:
		return "uint64", "cli.ParseUint[uint64]", true
	case *idltype.I8:
		return "int8", "cli.ParseInt[int8]", true
	case *idltype.I16:
		return "int16", "cli.ParseInt[int16]", true
	case *idltype.I32:
		return "int32", "cli.ParseInt[int32]", true
	case *idltype.I64:
		return "int64", "cli.ParseInt[int64]", true
	case *idltype.F32:
		return "float32", "cli.ParseFloat[float32]", true
	case *idltype.F64:
		return "float64", "cli.ParseFloat[float64]", true
	case *idltype.String:
		return "string", "cli.ParseString", true
	case *idltype.Pubkey:
		return "public-key", "cli.ParsePublicKey", true
	case *idltype.Bytes:
		return "bytes", "cli.ParseBytes", true
	}

	return "", "", false
}

func toKebab(s string) string {
	return strings.ReplaceAll(s, "_", "-")
}
