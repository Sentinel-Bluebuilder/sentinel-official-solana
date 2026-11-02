package main

import (
	"testing"
)

func TestParseSnapshotArgs_RequireFullAnchor(t *testing.T) {
	t.Parallel()

	for _, missing := range []string{"--header", "--commit", "--validator-set", "--chain-id"} {
		t.Run("missing "+missing, func(t *testing.T) {
			t.Parallel()

			var partial []string

			full := anchorFlags()
			for i := 0; i < len(full); i += 2 {
				if full[i] != missing {
					partial = append(partial, full[i], full[i+1])
				}
			}

			if _, err := parseVerifyArgs(append(partial, "--app-db", "/d/application.db")); err == nil {
				t.Fatalf("verify accepted args without %s", missing)
			}

			if _, err := parseSnapshotArgs(append(partial, "--app-db", "/d/a.db", "--out", "/d/l.bin")); err == nil {
				t.Fatalf("snapshot accepted args without %s", missing)
			}

			if _, err := parsePruneArgs(append(partial, "--app-db", "/d/a.db", "--height", "5000000")); err == nil {
				t.Fatalf("prune accepted args without %s", missing)
			}
		})
	}
}

func TestParseVerifyArgs(t *testing.T) {
	t.Parallel()

	a, err := parseVerifyArgs(append(anchorFlags(), "--app-db", "/d/application.db"))
	if err != nil {
		t.Fatal(err)
	}

	if a.AppDBPath != "/d/application.db" || a.ChainID != "sentinelhub-2" {
		t.Fatalf("parsed = %+v", a)
	}

	if _, err := parseVerifyArgs([]string{"--app-db", "/d/application.db", "--header", "/d/h.json"}); err == nil {
		t.Fatal("missing --commit/--validator-set/--chain-id must error")
	}

	if _, err := parseVerifyArgs(anchorFlags()); err == nil {
		t.Fatal("missing --app-db must error")
	}
}
