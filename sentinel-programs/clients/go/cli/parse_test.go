package cli_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sentinel-official/sentinel-programs/clients/go/cli"
)

type fakeUnmarshalerArray [2]uint8

func (a *fakeUnmarshalerArray) UnmarshalJSON([]byte) error {
	*a = fakeUnmarshalerArray{1, 2}
	return nil
}

func TestParseAccountMeta(t *testing.T) {
	m, err := cli.ParseAccountMeta("11111111111111111111111111111112:w:s")
	if err != nil || !m.IsWritable || !m.IsSigner || m.PublicKey.String() != "11111111111111111111111111111112" {
		t.Fatalf("account meta: %+v %v", m, err)
	}

	if _, err := cli.ParseAccountMeta("11111111111111111111111111111112:x"); err == nil {
		t.Fatal("unknown account flag must fail")
	}
}

func TestParseByteArray(t *testing.T) {
	if v, err := cli.ParseByteArray[[2]uint8]("US"); err != nil || v != [2]uint8{'U', 'S'} {
		t.Fatalf("array: %v %v", v, err)
	}
}

func TestParseBytes(t *testing.T) {
	if b, err := cli.ParseBytes("hi"); err != nil || string(b) != "hi" {
		t.Fatalf("text bytes: %q %v", b, err)
	}

	if b, err := cli.ParseBytes("0x0a0b"); err != nil || len(b) != 2 || b[1] != 0x0b {
		t.Fatalf("hex bytes: %v %v", b, err)
	}
}

func TestParseJSON(t *testing.T) {
	type item struct {
		N int `json:"n"`
	}

	if v, err := cli.ParseJSON[[]item](`[{"n":3}]`); err != nil || len(v) != 1 || v[0].N != 3 {
		t.Fatalf("inline json: %v %v", v, err)
	}

	path := filepath.Join(t.TempDir(), "items.json")
	if err := os.WriteFile(path, []byte(`[{"n":4}]`), 0o644); err != nil {
		t.Fatal(err)
	}

	if v, err := cli.ParseJSON[[]item]("@" + path); err != nil || len(v) != 1 || v[0].N != 4 {
		t.Fatalf("file json: %v %v", v, err)
	}

	type item2 struct {
		ID [2]uint8 `json:"id"`
	}

	for _, tc := range []struct {
		name string
		data string
		want string
	}{
		{"short", `[{"id":[1]}]`, "[0].id: array length mismatch: want 2, got 1"},
		{"long", `[{"id":[1,2,3]}]`, "[0].id: array length mismatch: want 2, got 3"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := cli.ParseJSON[[]item2](tc.data)
			if !errors.Is(err, cli.ErrArrayLengthMismatch) || err.Error() != tc.want {
				t.Fatalf("array length: %v", err)
			}
		})
	}

	if v, err := cli.ParseJSON[[]item2](`[{"id":[1,2]}]`); err != nil || v[0].ID != [2]uint8{1, 2} {
		t.Fatalf("exact array: %v %v", v, err)
	}

	type nested struct {
		ID   *[2]uint8 `json:"id"`
		Skip [2]uint8  `json:"-"`
	}
	_, err := cli.ParseJSON[map[string]*nested](`{"a":{"id":[1],"Skip":[1]}}`)
	if !errors.Is(err, cli.ErrArrayLengthMismatch) || err.Error() != "a.id: array length mismatch: want 2, got 1" {
		t.Fatalf("map pointer array: %v", err)
	}

	if v, err := cli.ParseJSON[nested](`{"ID":[1,2],"Skip":[1]}`); err != nil || v.ID == nil || *v.ID != [2]uint8{1, 2} {
		t.Fatalf("json field semantics: %v %v", v, err)
	}

	if v, err := cli.ParseJSON[fakeUnmarshalerArray](`[1]`); err != nil || v != (fakeUnmarshalerArray{1, 2}) {
		t.Fatalf("custom unmarshaler: %v %v", v, err)
	}

	if v, err := cli.ParseJSON[json.RawMessage](`[1]`); err != nil || string(v) != `[1]` {
		t.Fatalf("raw message: %s %v", v, err)
	}

	type Embedded struct {
		ID [2]uint8 `json:"id"`
	}
	type wrapper struct{ Embedded }
	for _, raw := range []string{`{"id":[1]}`, `{"id":[1,2,3]}`} {
		_, err := cli.ParseJSON[wrapper](raw)
		if !errors.Is(err, cli.ErrArrayLengthMismatch) || !strings.HasPrefix(err.Error(), "id: array length mismatch") {
			t.Fatalf("promoted field %s: %v", raw, err)
		}
	}

	type ambiguous struct {
		Foo [2]uint8 `json:"foo"`
		FOO [3]uint8 `json:"FOO"`
	}
	if v, err := cli.ParseJSON[ambiguous](`{"FOO":[1,2,3]}`); err != nil || v.FOO != [3]uint8{1, 2, 3} {
		t.Fatalf("exact field name: %v %v", v, err)
	}

	for _, raw := range []string{`[1] trailing`, ""} {
		_, err := cli.ParseJSON[[2]int](raw)
		var ignored [2]int
		want := json.Unmarshal([]byte(raw), &ignored)
		if errors.Is(err, cli.ErrArrayLengthMismatch) || err == nil || err.Error() != want.Error() {
			t.Fatalf("syntax %q: got %v, want %v", raw, err, want)
		}
	}
}
