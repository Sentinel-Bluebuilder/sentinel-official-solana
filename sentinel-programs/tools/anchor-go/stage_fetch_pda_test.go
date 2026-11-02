package main_test

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"testing"

	generator "github.com/sentinel-official/sentinel-programs/tools/anchor-go"
)

func TestWriteFetchPDA_ParameterShadowing(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile(filepath.Join("testdata", "idl.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}

	for _, value := range doc["instructions"].([]any) {
		ix := value.(map[string]any)
		for _, value := range ix["accounts"].([]any) {
			account := value.(map[string]any)
			if account["name"] != "record" {
				continue
			}
			pda, ok := account["pda"].(map[string]any)
			if !ok {
				continue
			}
			pda["seeds"].([]any)[1] = map[string]any{"kind": "arg", "path": "fmt"}
			ix["args"] = append(ix["args"].([]any), map[string]any{"name": "fmt", "type": "pubkey"})
		}
	}

	data, err = json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "idl.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	files, err := generator.Generate(path)
	if err != nil {
		t.Fatal(err)
	}
	file, err := parser.ParseFile(token.NewFileSet(), "fetch_pda.go", files["fetch_pda.go"], 0)
	if err != nil {
		t.Fatal(err)
	}

	checked := 0
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "FetchRecordBySeeds" {
			continue
		}
		checked++
		for _, param := range fn.Type.Params.List {
			for _, name := range param.Names {
				if name.Name == "fmt" {
					t.Fatal("seed parameter shadows the fmt import used by derivation errors")
				}
			}
		}
	}
	if checked != 2 {
		t.Fatalf("checked %d functions, want standalone and client methods", checked)
	}
}
