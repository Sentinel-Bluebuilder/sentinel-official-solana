package main

import (
	"encoding/binary"
	"fmt"
	"strconv"
	"strings"

	"github.com/gagliardetto/anchor-go/idl"
	"github.com/gagliardetto/solana-go"
)

const (
	accountKindParam accountKind = iota
	accountKindOptional
	accountKindProgram
	accountKindFixed
	accountKindPending
	accountKindDerived
)

type accountKind int

type builder struct {
	accounts []builderAccount
	args     []builderArg
	derived  []int
	ix       *idl.IdlInstruction
}

type builderAccount struct {
	acc   idl.IdlInstructionAccount
	expr  string
	field string
	find  string
	kind  accountKind
	local string
}

type builderArg struct {
	field  string
	goType string
	name   string
}

func allocateParamField(name string, fields map[string]bool) string {
	field := exportedName(name)
	for fields[field] {
		field += "Account"
	}

	fields[field] = true

	return field
}

func constArg(goType string, value []byte) (string, error) {
	switch {
	case goType == "uint8" && len(value) == 1:
		return fmt.Sprintf("%d", value[0]), nil
	case goType == "int8" && len(value) == 1:
		return fmt.Sprintf("int8(%d)", int8(value[0])), nil
	case goType == "uint16" && len(value) == 2:
		return fmt.Sprintf("uint16(%d)", binary.LittleEndian.Uint16(value)), nil
	case goType == "int16" && len(value) == 2:
		return fmt.Sprintf("int16(%d)", int16(binary.LittleEndian.Uint16(value))), nil
	case goType == "uint32" && len(value) == 4:
		return fmt.Sprintf("uint32(%d)", binary.LittleEndian.Uint32(value)), nil
	case goType == "int32" && len(value) == 4:
		return fmt.Sprintf("int32(%d)", int32(binary.LittleEndian.Uint32(value))), nil
	case goType == "uint64" && len(value) == 8:
		return fmt.Sprintf("uint64(%d)", binary.LittleEndian.Uint64(value)), nil
	case goType == "int64" && len(value) == 8:
		return fmt.Sprintf("int64(%d)", int64(binary.LittleEndian.Uint64(value))), nil
	case goType == "solana.PublicKey" && len(value) == 32:
		return "solana.MustPublicKeyFromBase58(" + strconv.Quote(solana.PublicKeyFromBytes(value).String()) + ")", nil
	case goType == "string":
		return strconv.Quote(string(value)), nil
	case goType == "[]byte":
		return byteLiteral(value), nil
	case isByteArray(goType) && fmt.Sprintf("[%d]uint8", len(value)) == goType:
		return goType + strings.TrimPrefix(byteLiteral(value), "[]byte"), nil
	}

	return "", fmt.Errorf("constant seed of %d bytes cannot be passed as %s", len(value), goType)
}

func deriveCall(ix *idl.IdlInstruction, acc idl.IdlInstructionAccount, spec pdaSpec, known, fetched map[string]string) (string, bool, error) {
	if spec.program == "program" {
		return "", false, nil
	}

	if acc.Pda.IsNone() {
		return "", false, fmt.Errorf("%s: pda %s: missing PDA", ix.Name, acc.Name)
	}

	pda := acc.Pda.Unwrap()
	if len(spec.inlined) != len(pda.Seeds) {
		return "", false, fmt.Errorf("%s: pda %s: seed layout mismatch", ix.Name, acc.Name)
	}

	variable := 0
	for _, inlined := range spec.inlined {
		if !inlined {
			variable++
		}
	}

	if len(spec.params) != variable {
		return "", false, fmt.Errorf("%s: pda %s: parameter layout mismatch", ix.Name, acc.Name)
	}

	args := []string{}

	if spec.program == "" {
		args = append(args, "programID")
	}

	consumed := 0

	for i, seed := range pda.Seeds {
		if spec.inlined[i] {
			if _, ok := seed.(*idl.IdlSeedConst); !ok {
				return "", false, fmt.Errorf("%s: pda %s: non-constant inline seed at position %d", ix.Name, acc.Name, i)
			}

			continue
		}

		param := spec.params[consumed]
		consumed++

		switch s := seed.(type) {
		case *idl.IdlSeedConst:
			lit, err := constArg(param.goType, s.Value)
			if err != nil {
				return "", false, fmt.Errorf("%s: pda %s: %w", ix.Name, acc.Name, err)
			}

			args = append(args, lit)
		case *idl.IdlSeedArg:
			path := strings.Split(s.Path, ".")
			found := false
			for _, arg := range ix.Args {
				if arg.Name == path[0] {
					found = true
					break
				}
			}

			if !found {
				return "", false, fmt.Errorf("%s: pda %s: arg %s not found", ix.Name, acc.Name, s.Path)
			}

			expr := "p"
			for _, seg := range strings.Split(s.Path, ".") {
				expr += "." + exportedName(seg)
			}

			args = append(args, expr)
		case *idl.IdlSeedAccount:
			if s.Account.IsSome() {
				root, _, _ := strings.Cut(s.Path, ".")
				local, ok := fetched[root]
				if !ok {
					return "", false, nil
				}

				args = append(args, local+".Value."+fieldPath(s.Path))

				continue
			}

			expr, ok := known[s.Path]
			if !ok {
				return "", false, nil
			}

			args = append(args, expr)
		default:
			return "", false, fmt.Errorf("%s: pda %s: unsupported seed %T", ix.Name, acc.Name, seed)
		}
	}

	return fmt.Sprintf("Find%sAddress(%s)", exportedName(spec.typeName), strings.Join(args, ", ")), true, nil
}

func fieldPath(path string) string {
	segs := strings.Split(path, ".")[1:]
	for i, seg := range segs {
		segs[i] = exportedName(seg)
	}

	return strings.Join(segs, ".")
}

func planBuilder(p *program, ix *idl.IdlInstruction, pdas map[string]pdaSpec, addresses map[string]string) (*builder, error) {
	accounts, err := flattenAccounts(ix.Accounts)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", ix.Name, err)
	}

	b := &builder{ix: ix}
	fields := map[string]bool{}

	for _, a := range ix.Args {
		gt, err := goType(p.doc, a.Ty)
		if err != nil {
			return nil, fmt.Errorf("%s: %s: %w", ix.Name, a.Name, err)
		}

		field := exportedName(a.Name)
		if fields[field] {
			return nil, fmt.Errorf("%s: argument %s has duplicate field %s", ix.Name, a.Name, field)
		}

		fields[field] = true
		b.args = append(b.args, builderArg{
			name:   a.Name,
			field:  field,
			goType: gt,
		})
	}

	known := map[string]string{}
	locals := map[string]bool{"p": true, "programID": true, "ix": true, "err": true}
	accountNames := map[string]bool{}

	for _, acc := range accounts {
		if accountNames[acc.Name] {
			return nil, fmt.Errorf("%s: duplicate account %s", ix.Name, acc.Name)
		}

		accountNames[acc.Name] = true
		local := unexportedName(acc.Name)
		for locals[local] {
			local += "Account"
		}

		locals[local] = true
		ba := builderAccount{
			acc:   acc,
			local: local,
		}

		switch {
		case acc.Optional:
			ba.kind = accountKindOptional
			ba.field = allocateParamField(acc.Name, fields)
			ba.expr = ba.local
		case acc.Address.IsSome() && p.idlAddress != nil && acc.Address.Unwrap().Equals(*p.idlAddress):
			ba.kind = accountKindProgram
			ba.expr = "programID"
			known[acc.Name] = ba.expr
		case acc.Address.IsSome():
			ba.kind = accountKindFixed
			ba.expr = addresses[acc.Name]
			if ba.expr == "" {
				return nil, fmt.Errorf("%s: account %s: fixed address not found", ix.Name, acc.Name)
			}

			known[acc.Name] = ba.expr
		case acc.Pda.IsSome():
			ba.kind = accountKindPending
		default:
			ba.kind = accountKindParam
			ba.field = allocateParamField(acc.Name, fields)
			ba.expr = "p." + ba.field
			known[acc.Name] = ba.expr
		}

		b.accounts = append(b.accounts, ba)
	}

	for {
		progress := false
		pending := -1

		for i := range b.accounts {
			ba := &b.accounts[i]
			if ba.kind != accountKindPending {
				continue
			}

			if pending < 0 {
				pending = i
			}

			spec, exists := pdas[ba.acc.Name]
			if !exists {
				return nil, fmt.Errorf("%s: pda %s: helper not found", ix.Name, ba.acc.Name)
			}

			find, ok, err := deriveCall(ix, ba.acc, spec, known, nil)
			if err != nil {
				return nil, err
			}

			if !ok {
				continue
			}

			ba.kind = accountKindDerived
			ba.find = find
			ba.expr = ba.local
			known[ba.acc.Name] = ba.expr
			b.derived = append(b.derived, i)
			progress = true
		}

		if pending < 0 {
			break
		}

		if !progress {
			ba := &b.accounts[pending]
			ba.kind = accountKindParam
			ba.field = allocateParamField(ba.acc.Name, fields)
			ba.expr = "p." + ba.field
			known[ba.acc.Name] = ba.expr
		}
	}

	return b, nil
}
