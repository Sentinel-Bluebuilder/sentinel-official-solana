package main

import (
	"bytes"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/gagliardetto/anchor-go/idl"
	"github.com/gagliardetto/anchor-go/idl/idltype"
	"github.com/gagliardetto/anchor-go/tools"
	"github.com/gagliardetto/solana-go"
)

var byteArrayPattern = regexp.MustCompile(`^\[\d+\]uint8$`)

type pdaParam struct {
	goType string
	name   string
}

type pdaSpec struct {
	inlined  []bool
	name     string
	params   []pdaParam
	prefix   string
	program  string
	seeds    []string
	typeName string
}

type pdaUse struct {
	first   *idl.IdlInstruction
	program idl.Option[idl.IdlSeed]
	sites   [][]seedSite
}

type seedSite struct {
	ix   *idl.IdlInstruction
	seed idl.IdlSeed
}

func agreedConst(sites []seedSite) ([]byte, bool) {
	var first []byte

	for i, s := range sites {
		c, ok := s.seed.(*idl.IdlSeedConst)
		if !ok {
			return nil, false
		}

		if i == 0 {
			first = c.Value

			continue
		}

		if !bytes.Equal(first, c.Value) {
			return nil, false
		}
	}

	return first, true
}

func argType(doc *idl.Idl, ix *idl.IdlInstruction, path string) (idltype.IdlType, error) {
	segs := strings.Split(path, ".")
	for _, seg := range segs {
		if seg == "" {
			return nil, fmt.Errorf("%s: invalid arg path %q", ix.Name, path)
		}
	}

	var t idltype.IdlType

	for _, a := range ix.Args {
		if a.Name == segs[0] {
			t = a.Ty
		}
	}

	if t == nil {
		return nil, fmt.Errorf("%s: arg path %s: argument %s not found", ix.Name, path, segs[0])
	}

	for _, seg := range segs[1:] {
		d, ok := t.(*idltype.Defined)
		if !ok {
			return nil, fmt.Errorf("%s: %s does not name a struct field", ix.Name, path)
		}

		if len(d.Generics) > 0 {
			return nil, fmt.Errorf("%s: %s: generic type %s is not supported", ix.Name, path, d.Name)
		}

		ft, err := fieldType(doc, d.Name, seg)
		if err != nil {
			return nil, fmt.Errorf("%s: %s: %w", ix.Name, path, err)
		}

		t = ft
	}

	return t, nil
}

func buildPDASpec(doc *idl.Idl, name string, use *pdaUse) (pdaSpec, error) {
	spec := pdaSpec{
		name:     name,
		typeName: pdaTypeName(doc, name, use),
	}
	used := map[string]bool{"programID": true, "program": true, "solana": true, "binary": true}

	for i, sites := range use.sites {
		if lit, ok := agreedConst(sites); ok {
			spec.inlined = append(spec.inlined, true)

			if i == 0 && isPrintable(lit) {
				spec.prefix = string(lit)
				spec.seeds = append(spec.seeds, "[]byte(Seed"+exportedName(spec.typeName)+")")
			} else {
				spec.seeds = append(spec.seeds, byteLiteral(lit))
			}

			continue
		}

		typ, pname, err := unifyPosition(doc, name, i, sites)
		if err != nil {
			return spec, err
		}

		for used[pname] {
			pname += strconv.Itoa(i)
		}

		used[pname] = true

		expr, err := seedExpr(typ, pname)
		if err != nil {
			return spec, fmt.Errorf("pda %s position %d: %w", name, i, err)
		}

		spec.inlined = append(spec.inlined, false)
		spec.params = append(spec.params, pdaParam{
			name:   pname,
			goType: typ,
		})
		spec.seeds = append(spec.seeds, expr)
	}

	if use.program.IsSome() {
		switch ps := use.program.Unwrap().(type) {
		case *idl.IdlSeedConst:
			if len(ps.Value) != 32 {
				return spec, fmt.Errorf("pda %s: program seed must contain 32 bytes, got %d", name, len(ps.Value))
			}

			spec.program = "solana.MustPublicKeyFromBase58(" + strconv.Quote(solana.PublicKeyFromBytes(ps.Value).String()) + ")"
		case *idl.IdlSeedAccount, *idl.IdlSeedArg:
			spec.params = append(spec.params, pdaParam{
				name:   "program",
				goType: "solana.PublicKey",
			})
			spec.program = "program"
		default:
			return spec, fmt.Errorf("pda %s: unsupported program seed %T", name, ps)
		}
	}

	return spec, nil
}

func byteLiteral(v []byte) string {
	parts := make([]string, len(v))
	for i, b := range v {
		parts[i] = strconv.Itoa(int(b))
	}

	return "[]byte{" + strings.Join(parts, ", ") + "}"
}

func collectPDAs(doc *idl.Idl) ([]pdaSpec, map[string]pdaSpec, error) {
	uses := map[string]*pdaUse{}
	order := []string{}

	for i := range doc.Instructions {
		ix := &doc.Instructions[i]

		accounts, err := flattenAccounts(ix.Accounts)
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", ix.Name, err)
		}

		for _, a := range accounts {
			if a.Pda.IsNone() {
				continue
			}

			pda := a.Pda.Unwrap()

			if pda.Program.IsSome() {
				seed := pda.Program.Unwrap()
				if _, constant := seed.(*idl.IdlSeedConst); !constant {
					typ, _, err := unifyPosition(doc, a.Name+" program", 0, []seedSite{{
						ix:   ix,
						seed: seed,
					}})
					if err != nil {
						return nil, nil, fmt.Errorf("%s: %w", ix.Name, err)
					}
					if typ != "solana.PublicKey" {
						return nil, nil, fmt.Errorf("%s: pda %s program seed must be a public key, got %s", ix.Name, a.Name, typ)
					}
				}
			}

			use, seen := uses[a.Name]
			if !seen {
				use = &pdaUse{
					sites:   make([][]seedSite, len(pda.Seeds)),
					program: pda.Program,
					first:   ix,
				}
				uses[a.Name] = use
				order = append(order, a.Name)
			}

			if len(pda.Seeds) != len(use.sites) {
				return nil, nil, fmt.Errorf("pda %s: %s has %d seeds, %s has %d", a.Name, use.first.Name, len(use.sites), ix.Name, len(pda.Seeds))
			}

			if !sameProgramSeed(use.program, pda.Program) {
				return nil, nil, fmt.Errorf("pda %s: program seed differs between %s and %s", a.Name, use.first.Name, ix.Name)
			}

			for j, s := range pda.Seeds {
				use.sites[j] = append(use.sites[j], seedSite{
					ix:   ix,
					seed: s,
				})
			}
		}
	}

	sort.Strings(order)

	specs := make([]pdaSpec, 0, len(order))
	byName := map[string]pdaSpec{}

	for _, name := range order {
		spec, err := buildPDASpec(doc, name, uses[name])
		if err != nil {
			return nil, nil, err
		}

		specs = append(specs, spec)
		byName[name] = spec
	}

	return specs, byName, nil
}

func isByteArray(goType string) bool {
	return byteArrayPattern.MatchString(goType)
}

func isCompatibleSeedConst(goType string, size int) bool {
	switch goType {
	case "string", "[]byte":
		return true
	case "uint8", "int8":
		return size == 1
	case "uint16", "int16":
		return size == 2
	case "uint32", "int32":
		return size == 4
	case "uint64", "int64":
		return size == 8
	case "solana.PublicKey":
		return size == 32
	}
	return goType == fmt.Sprintf("[%d]uint8", size)
}

func isPrintable(v []byte) bool {
	if len(v) == 0 {
		return false
	}

	for _, b := range v {
		if b < 0x20 || b > 0x7e {
			return false
		}
	}

	return true
}

func lastSegment(path string) string {
	parts := strings.Split(path, ".")

	return parts[len(parts)-1]
}

// pdaTypeName names a PDA after the IDL account type it addresses: the account whose name matches the instruction
// account, otherwise the one whose snake_case name is the PDA's leading constant seed. Without either the instruction
// account name stands.
func pdaTypeName(doc *idl.Idl, name string, use *pdaUse) string {
	for _, acc := range doc.Accounts {
		if exportedName(acc.Name) == exportedName(name) {
			return name
		}
	}

	if len(use.sites) == 0 {
		return name
	}

	lit, ok := agreedConst(use.sites[0])
	if !ok || !isPrintable(lit) {
		return name
	}

	for _, acc := range doc.Accounts {
		if tools.ToSnake(acc.Name) == string(lit) {
			return acc.Name
		}
	}

	return name
}

func sameProgramSeed(a, b idl.Option[idl.IdlSeed]) bool {
	if a.IsNone() || b.IsNone() {
		return a.IsNone() == b.IsNone()
	}

	ca, okA := a.Unwrap().(*idl.IdlSeedConst)
	cb, okB := b.Unwrap().(*idl.IdlSeedConst)

	if okA && okB {
		return bytes.Equal(ca.Value, cb.Value)
	}

	switch sa := a.Unwrap().(type) {
	case *idl.IdlSeedArg:
		sb, ok := b.Unwrap().(*idl.IdlSeedArg)
		return ok && sa.Path == sb.Path
	case *idl.IdlSeedAccount:
		sb, ok := b.Unwrap().(*idl.IdlSeedAccount)
		return ok && sa.Path == sb.Path && sa.Account.IsSome() == sb.Account.IsSome() && sa.Account.UnwrapOr("") == sb.Account.UnwrapOr("")
	default:
		return false
	}
}

func seedExpr(goType, name string) (string, error) {
	switch goType {
	case "uint8":
		return "[]byte{" + name + "}", nil
	case "int8":
		return "[]byte{byte(" + name + ")}", nil
	case "uint16":
		return "binary.LittleEndian.AppendUint16(nil, " + name + ")", nil
	case "int16":
		return "binary.LittleEndian.AppendUint16(nil, uint16(" + name + "))", nil
	case "uint32":
		return "binary.LittleEndian.AppendUint32(nil, " + name + ")", nil
	case "int32":
		return "binary.LittleEndian.AppendUint32(nil, uint32(" + name + "))", nil
	case "uint64":
		return "binary.LittleEndian.AppendUint64(nil, " + name + ")", nil
	case "int64":
		return "binary.LittleEndian.AppendUint64(nil, uint64(" + name + "))", nil
	case "string", "[]byte":
		return "[]byte(" + name + ")", nil
	case "solana.PublicKey":
		return name + "[:]", nil
	}

	if isByteArray(goType) {
		return name + "[:]", nil
	}

	return "", fmt.Errorf("unsupported seed type %s", goType)
}

func unifyPosition(doc *idl.Idl, name string, pos int, sites []seedSite) (string, string, error) {
	typ := ""
	pname := ""
	constLen := -1
	allOneByte := true
	constants := [][]byte{}

	for _, s := range sites {
		switch seed := s.seed.(type) {
		case *idl.IdlSeedConst:
			constLen = len(seed.Value)
			allOneByte = allOneByte && constLen == 1
			constants = append(constants, seed.Value)
		case *idl.IdlSeedArg:
			t, err := argType(doc, s.ix, seed.Path)
			if err != nil {
				return "", "", fmt.Errorf("pda %s: %w", name, err)
			}

			gt, err := goType(doc, t)
			if err != nil {
				return "", "", fmt.Errorf("pda %s: %s: %w", name, seed.Path, err)
			}

			if typ != "" && typ != gt {
				return "", "", fmt.Errorf("pda %s position %d: %s and %s disagree", name, pos, typ, gt)
			}

			typ = gt

			if !strings.HasPrefix(pname, "arg:") {
				pname = "arg:" + lastSegment(seed.Path)
			}
		case *idl.IdlSeedAccount:
			gt := "solana.PublicKey"

			if seed.Account.IsSome() {
				t, err := fieldType(doc, seed.Account.Unwrap(), lastSegment(seed.Path))
				if err != nil {
					return "", "", fmt.Errorf("pda %s: %s: %w", name, seed.Path, err)
				}

				gt, err = goType(doc, t)
				if err != nil {
					return "", "", fmt.Errorf("pda %s: %s: %w", name, seed.Path, err)
				}
			}

			if typ != "" && typ != gt {
				return "", "", fmt.Errorf("pda %s position %d: %s and %s disagree", name, pos, typ, gt)
			}

			typ = gt

			if pname == "" {
				pname = "account:" + lastSegment(seed.Path)
			}
		default:
			return "", "", fmt.Errorf("pda %s position %d: unsupported seed %T", name, pos, s.seed)
		}
	}

	if typ == "" {
		typ = "[]byte"
		if constLen == 1 && allOneByte {
			typ = "uint8"
		}
	}

	for _, value := range constants {
		if !isCompatibleSeedConst(typ, len(value)) {
			return "", "", fmt.Errorf("pda %s position %d: constant seed of %d bytes cannot be passed as %s", name, pos, len(value), typ)
		}
	}

	if pname == "" {
		pname = "seed" + strconv.Itoa(pos)
	} else {
		pname = unexportedName(pname[strings.Index(pname, ":")+1:])
	}

	return typ, pname, nil
}
