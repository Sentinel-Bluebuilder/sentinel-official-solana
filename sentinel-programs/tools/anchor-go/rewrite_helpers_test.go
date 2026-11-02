package main_test

import (
	"strings"
	"testing"

	generator "github.com/sentinel-official/sentinel-programs/tools/anchor-go"
)

func TestRewriteHelperCalls(t *testing.T) {
	src := []byte(`package x

import (
	"fmt"
	errors "github.com/gagliardetto/anchor-go/errors"
)

func f(err error) error {
	fmt.Println()
	if err != nil {
		return errors.NewField("Authority", errors.NewIndex(1, errors.NewOption("Maybe", err)))
	}
	return nil
}
`)

	out, err := generator.RewriteHelperCalls(src)
	if err != nil {
		t.Fatal(err)
	}

	got := string(out)
	if strings.Contains(got, "anchor-go/errors") {
		t.Fatalf("import left in:\n%s", got)
	}

	if !strings.Contains(got, `newFieldError("Authority", newIndexError(1, newOptionError("Maybe", err)))`) {
		t.Fatalf("calls not rewritten in:\n%s", got)
	}

	if !strings.Contains(got, `"fmt"`) {
		t.Fatalf("unrelated import dropped in:\n%s", got)
	}
}
