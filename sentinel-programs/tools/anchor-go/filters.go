package main

import (
	"fmt"

	"github.com/gagliardetto/anchor-go/idl"
)

const discriminatorSizeBytes = 8

type accountLayout struct {
	fields    []layoutField
	fixedSize bool
	name      string
	sizeBytes int
}

type layoutField struct {
	goType string
	name   string
	offset int
}

func accountLayouts(doc *idl.Idl) ([]accountLayout, error) {
	out := make([]accountLayout, 0, len(doc.Accounts))

	for _, acc := range doc.Accounts {
		fields, err := structFields(doc, acc.Name)
		if err != nil {
			return nil, fmt.Errorf("account %s: %w", acc.Name, err)
		}

		layout := accountLayout{
			name:      acc.Name,
			sizeBytes: discriminatorSizeBytes,
			fixedSize: true,
		}

		for _, f := range fields {
			n, fixed, err := borshSize(doc, f.Ty)
			if err != nil {
				return nil, fmt.Errorf("account %s.%s: %w", acc.Name, f.Name, err)
			}

			if !fixed {
				layout.fixedSize = false

				break
			}

			gt, err := goType(doc, f.Ty)
			if err != nil {
				return nil, fmt.Errorf("account %s.%s: %w", acc.Name, f.Name, err)
			}

			layout.fields = append(layout.fields, layoutField{
				name:   f.Name,
				goType: gt,
				offset: layout.sizeBytes,
			})
			layout.sizeBytes += n
		}

		out = append(out, layout)
	}

	return out, nil
}

func filterBytesExpr(goType, name string) (string, bool) {
	switch goType {
	case "solana.PublicKey":
		return name + "[:]", true
	case "bool":
		return "[]byte{boolByte(" + name + ")}", true
	case "uint8":
		return "[]byte{" + name + "}", true
	case "int8":
		return "[]byte{byte(" + name + ")}", true
	case "uint16", "int16", "uint32", "int32", "uint64", "int64":
		expr, err := seedExpr(goType, name)

		return expr, err == nil
	}

	if isByteArray(goType) {
		return name + "[:]", true
	}

	return "", false
}
