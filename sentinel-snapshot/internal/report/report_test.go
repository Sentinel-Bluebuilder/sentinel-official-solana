package report_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"cosmossdk.io/math"

	"github.com/sentinel-official/sentinel-snapshot/internal/report"
	"github.com/sentinel-official/sentinel-snapshot/internal/treasury"
)

func TestReconciliation_Marshal(t *testing.T) {
	t.Parallel()

	r := report.New(
		math.NewInt(1000),
		math.NewInt(800),
		math.NewInt(200),
		"deadbeef",
		math.NewInt(5),
		math.NewInt(7),
		nil,
		true,
	)

	b, err := r.Marshal()
	if err != nil {
		t.Fatal(err)
	}

	s := string(b)
	for _, want := range []string{
		`"supply_at_halt": "1000"`,
		`"total_claimable": "800"`,
		`"treasury_total": "200"`,
		`"ibc_fee_escrow": "5"`,
		`"non_leaf_attributed_escrow": "7"`,
		`"ledger_content_hash": "deadbeef"`,
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("reconciliation JSON missing %q:\n%s", want, s)
		}
	}
}

func TestReconciliation_Marshal_JSONKeys(t *testing.T) {
	t.Parallel()

	escrows := []treasury.ChannelEscrowAmount{{Amount: math.NewInt(3)}}
	wantKeys := []string{
		"ibc_escrow_by_channel",
		"ibc_escrow_total",
		"ibc_fee_escrow",
		"ledger_content_hash",
		"non_leaf_attributed_escrow",
		"supply_at_halt",
		"total_claimable",
		"treasury_total",
		"verified",
	}

	tests := []struct {
		name         string
		verified     bool
		wantVerified string
	}{
		{name: "verified", verified: true, wantVerified: "true"},
		{name: "not verified", verified: false, wantVerified: "false"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			reconciliation := report.New(
				math.NewInt(1000),
				math.NewInt(800),
				math.NewInt(200),
				"deadbeef",
				math.NewInt(5),
				math.NewInt(7),
				escrows,
				tt.verified,
			)

			encoded, err := reconciliation.Marshal()
			if err != nil {
				t.Fatal(err)
			}

			var fields map[string]json.RawMessage
			if err := json.Unmarshal(encoded, &fields); err != nil {
				t.Fatal(err)
			}

			gotKeys := make([]string, 0, len(fields))
			for key := range fields {
				gotKeys = append(gotKeys, key)
			}

			slices.Sort(gotKeys)

			if !slices.Equal(gotKeys, wantKeys) {
				t.Fatalf("reconciliation JSON keys = %v, want %v", gotKeys, wantKeys)
			}

			if got := string(fields["verified"]); got != tt.wantVerified {
				t.Fatalf("verified = %s, want %s (always present, no omitempty)", got, tt.wantVerified)
			}

			if got := string(fields["ibc_escrow_total"]); got != `"3"` {
				t.Fatalf("ibc_escrow_total = %s, want \"3\" (decimal string)", got)
			}
		})
	}
}
