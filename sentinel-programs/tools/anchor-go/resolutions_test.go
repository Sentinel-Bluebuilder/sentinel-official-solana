package main_test

import (
	"slices"
	"testing"

	generator "github.com/sentinel-official/sentinel-programs/tools/anchor-go"
)

func TestOrderResolutions(t *testing.T) {
	accounts := []string{"a", "b", "c", "d"}

	got, err := generator.OrderResolutionTargets("ix", accounts, []generator.ResolutionSpec{
		{Target: 3, Source: "b", Dependencies: []string{"b", "c"}},
		{Target: 2, Source: "a"},
		{Target: 0, Source: "b"},
	})
	if err != nil {
		t.Fatal(err)
	}

	if want := []int{0, 2, 3}; !slices.Equal(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}

	_, err = generator.OrderResolutionTargets("ix", accounts, []generator.ResolutionSpec{
		{Target: 0, Source: "b", Dependencies: []string{"b", "c"}},
		{Target: 2, Source: "a"},
	})
	if err == nil || err.Error() != "ix: account a: resolutions form a cycle" {
		t.Fatalf("cycle error = %v", err)
	}
}
