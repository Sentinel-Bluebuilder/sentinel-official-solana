package main

import (
	"fmt"

	"github.com/gagliardetto/anchor-go/idl"
)

func validateFields(doc *idl.Idl, owner string, fields idl.IdlDefinedFields) error {
	switch fs := fields.(type) {
	case nil:
		return nil
	case idl.IdlDefinedFieldsNamed:
		for _, field := range fs {
			if _, err := goType(doc, field.Ty); err != nil {
				return fmt.Errorf("type %s: field %s: %w", owner, field.Name, err)
			}
		}
	case idl.IdlDefinedFieldsTuple:
		for i, ty := range fs {
			if _, err := goType(doc, ty); err != nil {
				return fmt.Errorf("type %s: tuple position %d: %w", owner, i, err)
			}
		}
	default:
		return fmt.Errorf("type %s: unsupported fields %T", owner, fields)
	}

	return nil
}

func validateTypes(doc *idl.Idl) error {
	for _, td := range doc.Types {
		if len(td.Generics) > 0 {
			return fmt.Errorf("generic type %s is not supported", td.Name)
		}

		switch ty := td.Ty.(type) {
		case *idl.IdlTypeDefTyStruct:
			if err := validateFields(doc, td.Name, ty.Fields); err != nil {
				return err
			}
		case *idl.IdlTypeDefTyEnum:
			for _, variant := range ty.Variants {
				if variant.Fields.IsSome() {
					if err := validateFields(doc, td.Name+"."+variant.Name, variant.Fields.Unwrap()); err != nil {
						return err
					}
				}
			}
		default:
			return fmt.Errorf("type %s: unsupported definition %T", td.Name, td.Ty)
		}
	}

	for _, ix := range doc.Instructions {
		for _, arg := range ix.Args {
			if _, err := goType(doc, arg.Ty); err != nil {
				return fmt.Errorf("%s: arg %s: %w", ix.Name, arg.Name, err)
			}
		}
	}

	for _, c := range doc.Constants {
		if _, err := goType(doc, c.Ty); err != nil {
			return fmt.Errorf("constant %s: %w", c.Name, err)
		}
	}

	return nil
}
