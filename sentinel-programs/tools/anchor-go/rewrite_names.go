package main

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

var builtins = map[string]bool{
	"err": true, "nil": true, "true": true, "false": true, "len": true, "cap": true, "make": true, "new": true,
	"append": true, "copy": true, "string": true, "bool": true, "byte": true, "error": true, "int": true,
}

var (
	discriminatorName = regexp.MustCompile(`^(Account|Event|Instruction)_(.+)$`)
	instructionFunc   = regexp.MustCompile(`^New(.+)Instruction$`)
	jsonTag           = regexp.MustCompile(`json:"([^",]+)((?:,[a-z]+)*)"`)
	parserName        = regexp.MustCompile(`^Parse(Account|Event)_(.+)$`)
	versionElement    = regexp.MustCompile(`^v[0-9]+$`)
)

// applyNames renames identifiers and the string literals that spell a renamed
// identifier, leaving the selectors of imported packages alone. A non-nil
// words pattern also renames whole-word occurrences inside longer strings.
func applyNames(root ast.Node, names map[string]string, imports map[string]bool, words *regexp.Regexp) {
	skip := map[*ast.Ident]bool{}
	tags := map[*ast.BasicLit]bool{}

	ast.Inspect(root, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.SelectorExpr:
			if x, ok := n.X.(*ast.Ident); ok && imports[x.Name] && !isDeclared(x) {
				skip[n.Sel] = true
			}
		case *ast.Ident:
			if skip[n] {
				return true
			}

			if renamed, ok := names[n.Name]; ok {
				n.Name = renamed
			}
		case *ast.BasicLit:
			if n.Kind != token.STRING || tags[n] {
				return true
			}

			if s, err := strconv.Unquote(n.Value); err == nil {
				if renamed, ok := names[s]; ok {
					n.Value = strconv.Quote(renamed)
				} else if words != nil {
					if renamed := words.ReplaceAllStringFunc(s, func(w string) string { return names[w] }); renamed != s {
						n.Value = strconv.Quote(renamed)
					}
				}
			}
		case *ast.Field:
			if n.Tag != nil {
				tags[n.Tag] = true
				n.Tag.Value = snakeJSONTag(n.Tag.Value)
			}
		}

		return true
	})
}

func collectNames(f *ast.File, names map[string]string) {
	receivers := map[*ast.Ident]bool{}

	add := func(id *ast.Ident, rename func(string) string) {
		if id == nil || id.Name == "_" || receivers[id] {
			return
		}

		if renamed := rename(id.Name); renamed != id.Name {
			names[id.Name] = renamed
		}
	}

	ast.Inspect(f, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.FuncDecl:
			if n.Recv != nil {
				for _, field := range n.Recv.List {
					for _, id := range field.Names {
						receivers[id] = true
					}
				}
			}

			if n.Recv == nil && instructionFunc.MatchString(n.Name.Name) {
				add(n.Name, unexportedInstructionFunc)
			} else {
				add(n.Name, declaredName)
			}
		case *ast.TypeSpec:
			add(n.Name, declaredName)
		case *ast.ValueSpec:
			for _, id := range n.Names {
				add(id, declaredName)
			}
		case *ast.Field:
			for _, id := range n.Names {
				add(id, declaredName)
			}
		case *ast.AssignStmt:
			if n.Tok == token.DEFINE {
				for _, lhs := range n.Lhs {
					if id, ok := lhs.(*ast.Ident); ok {
						add(id, declaredName)
					}
				}
			}
		case *ast.RangeStmt:
			if n.Tok == token.DEFINE {
				for _, e := range []ast.Expr{n.Key, n.Value} {
					if id, ok := e.(*ast.Ident); ok {
						add(id, declaredName)
					}
				}
			}
		}

		return true
	})
}

func commentPattern(names map[string]string) *regexp.Regexp {
	olds := make([]string, 0, len(names))
	for old := range names {
		olds = append(olds, regexp.QuoteMeta(old))
	}

	if len(olds) == 0 {
		return nil
	}

	sort.Slice(olds, func(i, j int) bool { return len(olds[i]) > len(olds[j]) })

	return regexp.MustCompile(`\b(?:` + strings.Join(olds, "|") + `)\b`)
}

// declaredName returns the Go spelling of an identifier declared by the upstream output.
func declaredName(name string) string {
	if strings.HasPrefix(name, "_") || strings.HasSuffix(name, "_") || strings.Contains(name, "__") {
		return name
	}

	if m := discriminatorName.FindStringSubmatch(name); m != nil {
		return exportedName(m[2]) + m[1] + "Discriminator"
	}

	if m := parserName.FindStringSubmatch(name); m != nil {
		return "Parse" + exportedName(m[2]) + m[1]
	}

	if strings.HasSuffix(name, "EnumContainer") {
		name = strings.TrimSuffix(name, "EnumContainer") + "Container"
	}

	if unicode.IsUpper([]rune(name)[0]) {
		return exportedName(name)
	}

	return unexportedName(name)
}

func identifiers(root ast.Node) map[string]bool {
	out := map[string]bool{}

	ast.Inspect(root, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok {
			out[id.Name] = true
		}

		return true
	})

	return out
}

func importNames(f *ast.File) map[string]bool {
	out := map[string]bool{}

	for _, imp := range f.Imports {
		if imp.Name != nil {
			out[imp.Name.Name] = true

			continue
		}

		p, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			continue
		}

		base := path.Base(p)
		if versionElement.MatchString(base) {
			base = path.Base(path.Dir(p))
		}

		out[base] = true
	}

	return out
}

func isDeclared(id *ast.Ident) bool {
	return id.Obj != nil
}

func isKeywordOrBuiltin(name string) bool {
	return builtins[name] || token.IsKeyword(name)
}

func renameComments(f *ast.File, from, to token.Pos, names map[string]string) {
	pattern := commentPattern(names)

	for _, group := range f.Comments {
		for _, c := range group.List {
			if c.Pos() >= from && c.End() <= to {
				c.Text = pattern.ReplaceAllStringFunc(c.Text, func(s string) string { return names[s] })
			}
		}
	}
}

func renameFile(f *ast.File, names map[string]string, comments *regexp.Regexp) {
	imports := importNames(f)

	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}

		scoped := map[string]string{}
		if fn.Recv != nil {
			renameReceiver(fn, scoped)
		}

		if fn.Recv == nil && instructionFunc.MatchString(fn.Name.Name) {
			renameInstructionParams(fn, imports, scoped)
		}

		if len(scoped) > 0 {
			applyNames(fn, scoped, imports, nil)
			renameComments(f, fn.Pos(), fn.End(), scoped)
		}
	}

	applyNames(f, names, imports, comments)

	if comments != nil {
		for _, group := range f.Comments {
			for _, c := range group.List {
				c.Text = comments.ReplaceAllStringFunc(c.Text, func(s string) string { return names[s] })
			}
		}
	}
}

// renameInstructionParams drops the Param and Account suffixes the upstream
// generator appends to the arguments and accounts of an instruction builder.
func renameInstructionParams(fn *ast.FuncDecl, imports map[string]bool, scoped map[string]string) {
	used := identifiers(fn)

	for _, field := range fn.Type.Params.List {
		for _, id := range field.Names {
			base := strings.TrimSuffix(strings.TrimSuffix(id.Name, "Param"), "Account")
			if base == id.Name || base == "" {
				continue
			}

			renamed := unexportedName(base)
			if used[renamed] || imports[renamed] || isKeywordOrBuiltin(renamed) || strings.HasSuffix(renamed, "_") {
				continue
			}

			scoped[id.Name] = renamed
			used[renamed] = true
		}
	}
}

// renameReceiver replaces the generic receiver name with the initials of the type.
func renameReceiver(fn *ast.FuncDecl, scoped map[string]string) {
	field := fn.Recv.List[0]
	if len(field.Names) != 1 {
		return
	}

	old := field.Names[0].Name

	typeName := ""

	switch t := field.Type.(type) {
	case *ast.StarExpr:
		if id, ok := t.X.(*ast.Ident); ok {
			typeName = id.Name
		}
	case *ast.Ident:
		typeName = t.Name
	}

	if typeName == "" || old != "obj" {
		return
	}

	used := identifiers(fn)
	words := splitCamel(declaredName(typeName))

	var initials strings.Builder
	for _, w := range words {
		initials.WriteString(strings.ToLower(w[:1]))
	}

	for _, candidate := range []string{strings.ToLower(words[0][:1]), initials.String(), unexportedName(typeName)} {
		if !used[candidate] && !isKeywordOrBuiltin(candidate) {
			scoped[old] = candidate

			return
		}
	}
}

// renameUpstream applies the Go naming rules to the identifiers declared by the
// upstream generator output, which spells initialisms as words and joins enum
// variants and discriminators with underscores.
func renameUpstream(files map[string][]byte) error {
	fset := token.NewFileSet()
	parsed := map[string]*ast.File{}

	for name, src := range files {
		f, err := parser.ParseFile(fset, name, src, parser.ParseComments)
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}

		parsed[name] = f
	}

	names := map[string]string{}

	for _, f := range parsed {
		collectNames(f, names)
	}

	pattern := commentPattern(names)

	for name, f := range parsed {
		renameFile(f, names, pattern)

		var b bytes.Buffer
		if err := format.Node(&b, fset, f); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}

		files[name] = b.Bytes()
	}

	for name, src := range files {
		if renamed := strings.ReplaceAll(name, "-", "_"); renamed != name {
			files[renamed] = src

			delete(files, name)
		}
	}

	return nil
}

func snakeCase(s string) string {
	runes := []rune(s)

	var b strings.Builder

	for i, r := range runes {
		if i > 0 && unicode.IsUpper(r) && (unicode.IsLower(runes[i-1]) || unicode.IsDigit(runes[i-1]) || (unicode.IsUpper(runes[i-1]) && i+1 < len(runes) && unicode.IsLower(runes[i+1]))) {
			b.WriteByte('_')
		}

		b.WriteRune(unicode.ToLower(r))
	}

	return b.String()
}

func snakeJSONTag(tag string) string {
	return jsonTag.ReplaceAllStringFunc(tag, func(s string) string {
		m := jsonTag.FindStringSubmatch(s)

		return `json:"` + snakeCase(m[1]) + m[2] + `"`
	})
}

func unexportedInstructionFunc(name string) string {
	return "new" + exportedName(instructionFunc.FindStringSubmatch(name)[1]) + "Instruction"
}
