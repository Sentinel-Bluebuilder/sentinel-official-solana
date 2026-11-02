package main

import (
	"fmt"

	"github.com/gagliardetto/anchor-go/idl"
	"github.com/gagliardetto/anchor-go/idl/idltype"
)

func borshSize(doc *idl.Idl, t idltype.IdlType) (int, bool, error) {
	switch v := t.(type) {
	case *idltype.Bool, *idltype.U8, *idltype.I8:
		return 1, true, nil
	case *idltype.U16, *idltype.I16:
		return 2, true, nil
	case *idltype.U32, *idltype.I32, *idltype.F32:
		return 4, true, nil
	case *idltype.U64, *idltype.I64, *idltype.F64:
		return 8, true, nil
	case *idltype.U128, *idltype.I128:
		return 16, true, nil
	case *idltype.U256, *idltype.I256, *idltype.Pubkey:
		return 32, true, nil
	case *idltype.String, *idltype.Bytes, *idltype.Vec, *idltype.Option, *idltype.COption:
		return 0, false, nil
	case *idltype.Array:
		n, ok := v.Size.(*idltype.IdlArrayLenValue)
		if !ok {
			return 0, false, fmt.Errorf("generic array length is not supported")
		}

		elem, fixed, err := borshSize(doc, v.Type)
		if err != nil || !fixed {
			return 0, false, err
		}

		return n.Value * elem, true, nil
	case *idltype.Defined:
		if len(v.Generics) > 0 {
			return 0, false, fmt.Errorf("generic type %s is not supported", v.Name)
		}

		return definedSize(doc, v.Name)
	}

	return 0, false, fmt.Errorf("unsupported type %T", t)
}

func definedSize(doc *idl.Idl, name string) (int, bool, error) {
	td := doc.Types.ByName(name)
	if td == nil {
		return 0, false, fmt.Errorf("type %s not found", name)
	}

	if len(td.Generics) > 0 {
		return 0, false, fmt.Errorf("generic type %s is not supported", name)
	}

	switch ty := td.Ty.(type) {
	case *idl.IdlTypeDefTyEnum:
		for _, v := range ty.Variants {
			if v.Fields.IsSome() {
				return 0, false, nil
			}
		}

		return 1, true, nil
	case *idl.IdlTypeDefTyStruct:
		total := 0

		switch fields := ty.Fields.(type) {
		case idl.IdlDefinedFieldsNamed:
			for _, f := range fields {
				n, fixed, err := borshSize(doc, f.Ty)
				if err != nil {
					return 0, false, fmt.Errorf("type %s.%s: %w", name, f.Name, err)
				}

				if !fixed {
					return 0, false, nil
				}

				total += n
			}
		case idl.IdlDefinedFieldsTuple:
			for i, t := range fields {
				n, fixed, err := borshSize(doc, t)
				if err != nil {
					return 0, false, fmt.Errorf("type %s field %d: %w", name, i, err)
				}

				if !fixed {
					return 0, false, nil
				}

				total += n
			}
		}

		return total, true, nil
	}

	return 0, false, fmt.Errorf("type %s: unsupported definition %T", name, td.Ty)
}

func fieldType(doc *idl.Idl, typeName, field string) (idltype.IdlType, error) {
	fields, err := structFields(doc, typeName)
	if err != nil {
		return nil, err
	}

	for _, f := range fields {
		if f.Name == field {
			return f.Ty, nil
		}
	}

	return nil, fmt.Errorf("field %s not found on %s", field, typeName)
}

func goType(doc *idl.Idl, t idltype.IdlType) (string, error) {
	switch v := t.(type) {
	case *idltype.Bool:
		return "bool", nil
	case *idltype.U8:
		return "uint8", nil
	case *idltype.I8:
		return "int8", nil
	case *idltype.U16:
		return "uint16", nil
	case *idltype.I16:
		return "int16", nil
	case *idltype.U32:
		return "uint32", nil
	case *idltype.I32:
		return "int32", nil
	case *idltype.U64:
		return "uint64", nil
	case *idltype.I64:
		return "int64", nil
	case *idltype.F32:
		return "float32", nil
	case *idltype.F64:
		return "float64", nil
	case *idltype.U128:
		return "bin.Uint128", nil
	case *idltype.I128:
		return "bin.Int128", nil
	case *idltype.Pubkey:
		return "solana.PublicKey", nil
	case *idltype.String:
		return "string", nil
	case *idltype.Bytes:
		return "[]byte", nil
	case *idltype.Array:
		n, ok := v.Size.(*idltype.IdlArrayLenValue)
		if !ok {
			return "", fmt.Errorf("generic array length %s is not supported", v.Size)
		}

		if n.Value < 0 {
			return "", fmt.Errorf("array length %d is invalid", n.Value)
		}

		elem, err := goType(doc, v.Type)
		if err != nil {
			return "", err
		}

		return fmt.Sprintf("[%d]%s", n.Value, elem), nil
	case *idltype.Vec:
		elem, err := goType(doc, v.Vec)
		if err != nil {
			return "", err
		}

		return "[]" + elem, nil
	case *idltype.Option:
		elem, err := goType(doc, v.Option)
		if err != nil {
			return "", err
		}

		return "*" + elem, nil
	case *idltype.COption:
		elem, err := goType(doc, v.COption)
		if err != nil {
			return "", err
		}

		return "*" + elem, nil
	case *idltype.Defined:
		td := doc.Types.ByName(v.Name)
		if td == nil {
			return "", fmt.Errorf("type %s not found", v.Name)
		}

		if len(v.Generics) > 0 || len(td.Generics) > 0 {
			return "", fmt.Errorf("generic type %s is not supported", v.Name)
		}

		return exportedName(v.Name), nil
	}

	return "", fmt.Errorf("unsupported type %T", t)
}

func structFields(doc *idl.Idl, typeName string) (idl.IdlDefinedFieldsNamed, error) {
	td := doc.Types.ByName(typeName)
	if td == nil {
		return nil, fmt.Errorf("type %s not found", typeName)
	}

	if len(td.Generics) > 0 {
		return nil, fmt.Errorf("generic type %s is not supported", typeName)
	}

	st, ok := td.Ty.(*idl.IdlTypeDefTyStruct)
	if !ok {
		return nil, fmt.Errorf("type %s is not a struct", typeName)
	}

	fields, ok := st.Fields.(idl.IdlDefinedFieldsNamed)
	if !ok {
		return nil, fmt.Errorf("type %s has no named fields", typeName)
	}

	return fields, nil
}
