package main

import (
	"fmt"

	"github.com/gagliardetto/anchor-go/idl"
)

func flattenAccounts(items []idl.IdlInstructionAccountItem) ([]idl.IdlInstructionAccount, error) {
	out := make([]idl.IdlInstructionAccount, 0, len(items))

	for _, it := range items {
		switch a := it.(type) {
		case *idl.IdlInstructionAccount:
			out = append(out, *a)
		case *idl.IdlInstructionAccounts:
			return nil, fmt.Errorf("nested account groups are not supported")
		default:
			return nil, fmt.Errorf("unknown account item %T", it)
		}
	}

	return out, nil
}

func validateAccounts(doc *idl.Idl) error {
	for _, ix := range doc.Instructions {
		if _, err := flattenAccounts(ix.Accounts); err != nil {
			return fmt.Errorf("%s: %w", ix.Name, err)
		}
	}

	return nil
}
