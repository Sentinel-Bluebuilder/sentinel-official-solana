package main

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"

	"github.com/gagliardetto/anchor-go/idl/idltype"
	"github.com/gagliardetto/anchor-go/tools"
)

const commandsPrelude = `func cmdContext(cmd *cobra.Command) context.Context {
	if ctx := cmd.Context(); ctx != nil {
		return ctx
	}

	return context.Background()
}

`

func commandImports(body string) (string, error) {
	if strings.TrimSpace(body) == "" {
		return "", nil
	}

	file, err := parser.ParseFile(token.NewFileSet(), "commands.go", "package cmd\n"+body, 0)
	if err != nil {
		return "", fmt.Errorf("command imports: %w", err)
	}

	selectors := make(map[string]bool)
	ast.Inspect(file, func(n ast.Node) bool {
		if selector, ok := n.(*ast.SelectorExpr); ok {
			if name, ok := selector.X.(*ast.Ident); ok {
				selectors[name.Name] = true
			}
		}

		return true
	})

	var b strings.Builder

	b.WriteString("import (\n\t\"context\"\n")

	if selectors["fmt"] {
		b.WriteString("\t\"fmt\"\n")
	}

	b.WriteString("\n")
	fmt.Fprintf(&b, "\t%q\n", modulePath+"/cli")

	if selectors["bin"] {
		b.WriteString("\tbin \"github.com/gagliardetto/binary\"\n")
	}

	if selectors["solana"] {
		b.WriteString("\t\"github.com/solana-foundation/solana-go/v2\"\n")
	}

	if selectors["rpc"] {
		b.WriteString("\t\"github.com/solana-foundation/solana-go/v2/rpc\"\n")
	}

	b.WriteString("\t\"github.com/spf13/cobra\"\n)\n\n")

	return b.String(), nil
}

func instructionArgType(b *builder, name string) idltype.IdlType {
	for _, a := range b.ix.Args {
		if a.Name == name {
			return a.Ty
		}
	}

	return nil
}

func isTarget(resolutions []resolution, i int) bool {
	for _, f := range resolutions {
		if f.target == i {
			return true
		}
	}

	return false
}

func renderCommands(p *program) ([]byte, error) {
	if len(p.doc.Instructions) == 0 && len(p.doc.Accounts) == 0 {
		return []byte("package " + p.pkg + "\n"), nil
	}

	_, pdas, err := collectPDAs(p.doc)
	if err != nil {
		return nil, err
	}

	_, addresses, err := collectAddresses(p)
	if err != nil {
		return nil, err
	}

	layouts, err := accountLayouts(p.doc)
	if err != nil {
		return nil, err
	}

	constructors := make(map[string]string)
	commands := make(map[string]string)
	register := func(name, command, owner string) error {
		if prior, ok := constructors[name]; ok {
			return fmt.Errorf("%s: constructor %s collides with %s", owner, name, prior)
		}

		if prior, ok := commands[command]; ok {
			return fmt.Errorf("%s: command %s collides with %s", owner, command, prior)
		}

		constructors[name] = owner
		commands[command] = owner

		return nil
	}

	var body bytes.Buffer

	body.WriteString(commandsPrelude)

	for i := range p.doc.Instructions {
		ix := &p.doc.Instructions[i]

		if err := register("new"+exportedName(ix.Name)+"Cmd", toKebab(ix.Name), "instruction "+ix.Name); err != nil {
			return nil, err
		}

		b, err := planBuilder(p, ix, pdas, addresses)
		if err != nil {
			return nil, err
		}

		resolutions, err := collectResolutions(p, b, pdas)
		if err != nil {
			return nil, err
		}

		if err := writeCommand(&body, p, b, resolutions); err != nil {
			return nil, err
		}
	}

	for _, l := range layouts {
		for _, prefix := range []string{"show", "search"} {
			if err := register("new"+exportedName(prefix)+exportedName(l.name)+"Cmd", prefix+"-"+toKebab(tools.ToSnake(l.name)), "account "+l.name); err != nil {
				return nil, err
			}
		}

		if err := writeQueries(&body, p, l); err != nil {
			return nil, err
		}
	}

	body.WriteString("func Commands(r cli.Runner) []*cobra.Command {\n\treturn []*cobra.Command{\n")

	for _, ix := range p.doc.Instructions {
		fmt.Fprintf(&body, "\t\tnew%sCmd(r),\n", exportedName(ix.Name))
	}

	for _, l := range layouts {
		fmt.Fprintf(&body, "\t\tnewShow%[1]sCmd(r),\n\t\tnewSearch%[1]sCmd(r),\n", exportedName(l.name))
	}

	body.WriteString("\t}\n}\n")

	var b bytes.Buffer

	fmt.Fprintf(&b, "package %s\n\n", p.pkg)
	imports, err := commandImports(body.String())
	if err != nil {
		return nil, err
	}

	b.WriteString(imports)
	b.Write(body.Bytes())

	return b.Bytes(), nil
}

func validateCommandNames(files map[string][]byte) error {
	commands, err := parser.ParseFile(token.NewFileSet(), "commands.go", files["commands.go"], 0)
	if err != nil {
		return err
	}

	for name, src := range files {
		if name == "commands.go" {
			continue
		}

		file, err := parser.ParseFile(token.NewFileSet(), name, src, 0)
		if err != nil {
			return err
		}

		for symbol := range commands.Scope.Objects {
			if file.Scope.Lookup(symbol) != nil {
				return fmt.Errorf("commands.go: symbol %s collides with %s", symbol, name)
			}
		}
	}

	return nil
}

func writeCommand(w *bytes.Buffer, p *program, b *builder, resolutions []resolution) error {
	name := exportedName(b.ix.Name)

	var fs flagPlan

	signerIndex := -1

	for i, ba := range b.accounts {
		if ba.acc.Signer {
			signerIndex = i

			break
		}
	}

	if signerIndex >= 0 && b.accounts[signerIndex].kind != accountKindParam {
		return fmt.Errorf("%s: first signer %s is not a plain account", b.ix.Name, b.accounts[signerIndex].acc.Name)
	}

	var signerDefaults []string

	for _, a := range b.args {
		argType := instructionArgType(b, a.name)
		flag := toKebab(a.name)

		if isRentRecipientArg(a.name, argType) {
			stmt := fmt.Sprintf(
				"cli.Var(cmd.Flags(), &p.%s, %q, %q, \"address\", cli.ParsePublicKey)",
				a.field,
				flag,
				phrase(a.name),
			)
			fs.add(flag, stmt, signerIndex < 0)
			signerDefaults = append(signerDefaults, a.field)

			continue
		}

		if err := planFlags(p, flag, "p."+a.field, argType, phrase(a.name), &fs); err != nil {
			return fmt.Errorf("%s: %s: %w", b.ix.Name, a.name, err)
		}
	}

	for i, ba := range b.accounts {
		flag := toKebab(ba.acc.Name)

		switch {
		case ba.kind == accountKindParam && i != signerIndex:
			fs.add(flag, fmt.Sprintf("cli.Var(cmd.Flags(), &p.%s, %q, %q, \"address\", cli.ParsePublicKey)", ba.field, flag, phrase(ba.acc.Name)), false)
		case ba.kind == accountKindOptional:
			fs.add(flag, fmt.Sprintf("cli.OptionalVar(cmd.Flags(), &p.%s, %q, %q, \"address\", cli.ParsePublicKey)", ba.field, flag, phrase(ba.acc.Name)), false)
		}
	}

	fs.add("remaining-account", `cli.SliceVar(cmd.Flags(), &p.RemainingAccounts, "remaining-account", "remaining account as public-key[:w][:s]", "account-meta", cli.ParseAccountMeta)`, false)

	flags := make(map[string]bool)
	for _, fb := range fs.bindings {
		if flags[fb.name] {
			return fmt.Errorf("%s: duplicate flag --%s", b.ix.Name, fb.name)
		}

		flags[fb.name] = true
	}

	locals := make(map[string]bool)
	for _, local := range fs.declarations {
		name := strings.Fields(local)[1]
		if locals[name] {
			return fmt.Errorf("%s: duplicate local %s", b.ix.Name, name)
		}

		locals[name] = true
	}

	fmt.Fprintf(w, "func new%sCmd(r cli.Runner) *cobra.Command {\n\tvar p %sParams\n", name, name)

	for _, l := range fs.declarations {
		fmt.Fprintf(w, "\t%s\n", l)
	}

	fmt.Fprintf(w, "\n\tcmd := &cobra.Command{\n\t\tUse:   %q,\n\t\tShort: %q,\n\t\tArgs:  cobra.NoArgs,\n\t}\n\n", toKebab(b.ix.Name), phrase(b.ix.Name))

	for _, fb := range fs.bindings {
		fmt.Fprintf(w, "\t%s\n", fb.stmt)
	}

	w.WriteString("\n\tcmd.RunE = func(cmd *cobra.Command, _ []string) error {\n\t\tctx := cmdContext(cmd)\n\n")

	required := []string{}

	for _, fb := range fs.bindings {
		if fb.required && fb.condition == "" {
			required = append(required, strconv.Quote(fb.name))
		}
	}

	if len(required) > 0 {
		fmt.Fprintf(w, "\t\tif err := cli.Require(cmd.Flags(), %s); err != nil {\n\t\t\treturn err\n\t\t}\n\n", strings.Join(required, ", "))
	}

	for _, fb := range fs.bindings {
		if fb.required && fb.condition != "" {
			fmt.Fprintf(w, "\t\tif %s {\n\t\t\tif err := cli.Require(cmd.Flags(), %q); err != nil {\n\t\t\t\treturn err\n\t\t\t}\n\t\t}\n\n", fb.condition, fb.name)
		}
	}

	for _, s := range fs.postStatements {
		fmt.Fprintf(w, "\t\t%s\n\n", s)
	}

	if signerIndex >= 0 {
		writeSignerDefaults(w, b, signerIndex, signerDefaults)
	}

	for i, ba := range b.accounts {
		if i == signerIndex || ba.acc.Signer {
			continue
		}

		switch ba.kind {
		case accountKindParam:
			fmt.Fprintf(w, "\t\tif p.%s.IsZero() {\n\t\t\tif k, ok := r.Account(%q); ok {\n\t\t\t\tp.%s = k\n\t\t\t}\n\t\t}\n\n", ba.field, ba.acc.Name, ba.field)
		case accountKindOptional:
			fmt.Fprintf(w, "\t\tif p.%s == nil {\n\t\t\tif k, ok := r.Account(%q); ok {\n\t\t\t\tp.%s = &k\n\t\t\t}\n\t\t}\n\n", ba.field, ba.acc.Name, ba.field)
		}
	}

	for i, ba := range b.accounts {
		if ba.kind != accountKindParam || (!ba.acc.Signer && isTarget(resolutions, i)) {
			continue
		}

		fmt.Fprintf(w, "\t\tif p.%s.IsZero() {\n\t\t\treturn fmt.Errorf(\"%%w: --%%s\", cli.ErrMissingFlag, %q)\n\t\t}\n\n", ba.field, toKebab(ba.acc.Name))
	}

	if len(resolutions) > 0 {
		fmt.Fprintf(w, "\t\tix, err := Build%sInstructionOnChain(ctx, r.RPC(), r.Options(), r.ProgramID(), p)\n", name)
	} else {
		fmt.Fprintf(w, "\t\tix, err := Build%sInstruction(r.ProgramID(), p)\n", name)
	}

	w.WriteString("\t\tif err != nil {\n\t\t\treturn err\n\t\t}\n\n")

	for i, ba := range b.accounts {
		if ba.kind != accountKindParam {
			continue
		}

		fmt.Fprintf(w, "\t\tif k := ix.Accounts()[%d]; k == nil || k.PublicKey.IsZero() {\n\t\t\treturn fmt.Errorf(\"%%w: --%%s\", cli.ErrMissingFlag, %q)\n\t\t}\n\n", i, toKebab(ba.acc.Name))
	}

	fmt.Fprintf(w, "\t\treturn r.Run(ctx, %q, ix)\n\t}\n\n\treturn cmd\n}\n\n", b.ix.Name)

	return nil
}

func writeQueries(w *bytes.Buffer, p *program, l accountLayout) error {
	name := exportedName(l.name)
	command := toKebab(tools.ToSnake(l.name))

	fmt.Fprintf(w, "func newShow%sCmd(r cli.Runner) *cobra.Command {\n\tcmd := &cobra.Command{\n\t\tUse:   %q,\n\t\tShort: %q,\n\t\tArgs:  cobra.ExactArgs(1),\n\t}\n\n", name, "show-"+command+" <address>", "show "+phrase(l.name))
	fmt.Fprintf(w, "\tcmd.RunE = func(cmd *cobra.Command, args []string) error {\n\t\taddress, err := solana.PublicKeyFromBase58(args[0])\n\t\tif err != nil {\n\t\t\treturn err\n\t\t}\n\n\t\tv, err := Fetch%s(cmdContext(cmd), r.RPC(), address, r.Options())\n\t\tif err != nil {\n\t\t\treturn err\n\t\t}\n\n\t\treturn r.Print(v)\n\t}\n\n\treturn cmd\n}\n\n", name)

	var fs flagPlan

	applies := []string{}
	filters := make(map[string]string)

	for _, f := range l.fields {
		if _, ok := filterBytesExpr(f.goType, "v"); !ok {
			continue
		}

		filter := filterName(name, f.name)
		if prior, ok := filters[filter]; ok {
			return fmt.Errorf("account %s: field %s: filter %s collides with field %s", l.name, f.name, filter, prior)
		}

		filters[filter] = f.name

		ty, err := fieldType(p.doc, l.name, f.name)
		if err != nil {
			return fmt.Errorf("account %s: field %s: %w", l.name, f.name, err)
		}

		qt, err := goType(p.doc, ty)
		if err != nil {
			return fmt.Errorf("account %s: field %s: %w", l.name, f.name, err)
		}

		local := fs.allocateLocal("f" + exportedName(f.name))
		fs.declarations = append(fs.declarations, fmt.Sprintf("var %s %s", local, qt))

		if err := planFlags(p, toKebab(f.name), local, ty, phrase(f.name), &fs); err != nil {
			return fmt.Errorf("account %s: field %s: %w", l.name, f.name, err)
		}

		applies = append(applies, fmt.Sprintf("if cmd.Flags().Changed(%q) {\n\t\t\tfilters = append(filters, %s(%s))\n\t\t}", toKebab(f.name), filter, local))
	}

	flags := make(map[string]bool)
	for _, fb := range fs.bindings {
		if flags[fb.name] {
			return fmt.Errorf("account %s: duplicate flag --%s", l.name, fb.name)
		}

		flags[fb.name] = true
	}

	fmt.Fprintf(w, "func newSearch%sCmd(r cli.Runner) *cobra.Command {\n", name)

	for _, local := range fs.declarations {
		fmt.Fprintf(w, "\t%s\n", local)
	}

	fmt.Fprintf(w, "\n\tcmd := &cobra.Command{\n\t\tUse:   %q,\n\t\tShort: %q,\n\t\tArgs:  cobra.NoArgs,\n\t}\n\n", "search-"+command, "search "+phrase(l.name)+" accounts")

	for _, fb := range fs.bindings {
		fmt.Fprintf(w, "\t%s\n", fb.stmt)
	}

	w.WriteString("\n\tcmd.RunE = func(cmd *cobra.Command, _ []string) error {\n\t\tfilters := []rpc.RPCFilter{}\n\n")

	for _, a := range applies {
		fmt.Fprintf(w, "\t\t%s\n\n", a)
	}

	fmt.Fprintf(w, "\t\tout, err := Find%sAccounts(cmdContext(cmd), r.RPC(), r.ProgramID(), r.Options(), filters...)\n\t\tif err != nil {\n\t\t\treturn err\n\t\t}\n\n\t\treturn r.Print(out)\n\t}\n\n\treturn cmd\n}\n\n", name)

	return nil
}

func writeSignerDefaults(w *bytes.Buffer, b *builder, signerIndex int, signerDefaults []string) {
	fmt.Fprintf(w, "\t\tsigner, err := r.Signer(ctx)\n\t\tif err != nil {\n\t\t\treturn err\n\t\t}\n\n\t\tp.%s = signer\n\n", b.accounts[signerIndex].field)

	for i, ba := range b.accounts {
		if ba.kind == accountKindParam && ba.acc.Signer && i != signerIndex {
			fmt.Fprintf(w, "\t\tif p.%[1]s.IsZero() {\n\t\t\tif k, ok := r.Account(%[2]q); ok {\n\t\t\t\tp.%[1]s = k\n\t\t\t} else {\n\t\t\t\tp.%[1]s = signer\n\t\t\t}\n\t\t}\n\n", ba.field, ba.acc.Name)
		}
	}

	for _, field := range signerDefaults {
		fmt.Fprintf(w, "\t\tif p.%s.IsZero() {\n\t\t\tp.%s = signer\n\t\t}\n\n", field, field)
	}
}
