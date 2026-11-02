package core_test

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/sentinel-official/sentinel-programs/clients/go/core"
	"github.com/sentinel-official/sentinel-programs/clients/go/distributor"
	"github.com/sentinel-official/sentinel-programs/clients/go/dvpn"
	"github.com/solana-foundation/solana-go/v2"
	"github.com/solana-foundation/solana-go/v2/rpc"
)

func TestDecodeTxError(t *testing.T) {
	dvpnProgram := solana.MustPublicKeyFromBase58("SentineLDVPN1111111111111111111111111111111")
	programs := []solana.PublicKey{solana.SystemProgramID, solana.Token2022ProgramID, dvpnProgram}
	cases := []struct {
		name string
		err  any
		logs []string
		want string
	}{
		{"system already in use", fakeInstructionError(0, map[string]any{"Custom": 0}), nil,
			"system error: AccountAlreadyInUse (0): account already in use"},
		{"token owner mismatch", fakeInstructionError(1, map[string]any{"Custom": 4}), nil,
			"token-2022 error: OwnerMismatch (4): owner does not match"},
		{"token fixed supply", fakeInstructionError(1, map[string]any{"Custom": 5}), nil,
			"token-2022 error: FixedSupply (5): the total supply of this token is fixed"},
		{"token already in use", fakeInstructionError(1, map[string]any{"Custom": 6}), nil,
			"token-2022 error: AlreadyInUse (6): account or token already in use"},
		{"token invalid state", fakeInstructionError(1, map[string]any{"Custom": 13}), nil,
			"token-2022 error: InvalidState (13): invalid account state for operation"},
		{"token decimals", fakeInstructionError(1, map[string]any{"Custom": 18}), nil,
			"token-2022 error: MintDecimalsMismatch (18): decimals different from the Mint decimals"},
		{"token last legacy code", fakeInstructionError(1, map[string]any{"Custom": 19}), nil,
			"token-2022 error: NonNativeNotSupported (19): instruction does not support non-native tokens"},
		{"system nonce code", fakeInstructionError(0, map[string]any{"Custom": 8}), nil,
			"system error: NonceUnexpectedBlockhashValue (8): specified nonce does not match stored nonce"},
		{"anchor constraint", fakeInstructionError(2, map[string]any{"Custom": 2006}),
			[]string{"Program log: AnchorError caused by account: node. Error Code: ConstraintSeeds. Error Number: 2006. Error Message: A seeds constraint was violated."},
			dvpnProgram.String() + " error: ConstraintSeeds (2006): A seeds constraint was violated."},
		{"runtime", fakeInstructionError(0, "ReadonlyDataModified"), nil,
			"system error: ReadonlyDataModified (0): "},
		{"account not found", fakeInstructionError(0, "AccountNotFound"), nil,
			"system error: AccountNotFound (0): "},
		{"unknown transaction", "AccountNotFound", nil, "AccountNotFound"},
		{"unknown custom", fakeInstructionError(2, map[string]any{"Custom": 6100}), nil,
			dvpnProgram.String() + " error: " + fmt.Sprint(fakeInstructionError(2, map[string]any{"Custom": 6100}))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := core.DecodeTxError(tc.err, tc.logs, programs)
			if got == nil || got.Error() != tc.want {
				t.Fatalf("got %v, want %q", got, tc.want)
			}
		})
	}
	if got := core.DecodeTxError(nil, nil, programs); got != nil {
		t.Fatalf("nil error: %v", got)
	}
}

func TestDecodeTxError_CaughtCPI(t *testing.T) {
	outer := solana.MustPublicKeyFromBase58("11111111111111111111111111111113")
	inner := solana.MustPublicKeyFromBase58("11111111111111111111111111111114")
	logs := []string{
		"Program " + outer.String() + " invoke [1]",
		"Program " + solana.Token2022ProgramID.String() + " invoke [2]",
		"Program " + solana.Token2022ProgramID.String() + " failed: custom program error: 0x1",
		"Program log: handled token failure",
		"Program " + inner.String() + " invoke [2]",
		"Program log: AnchorError occurred. Error Code: InnerError. Error Number: 1. Error Message: handled.",
		"Program " + inner.String() + " failed: custom program error: 0x1",
		"Program log: handled inner failure",
		"Program log: AnchorError occurred. Error Code: OuterError. Error Number: 1. Error Message: outer failed.",
		"Program " + outer.String() + " failed: custom program error: 0x1",
	}

	got := core.DecodeTxError(fakeInstructionError(0, map[string]any{"Custom": 1}), logs, []solana.PublicKey{outer})
	if got == nil || got.Program != outer.String() || got.Name != "OuterError" || got.Code != 1 || got.Message != "outer failed." {
		t.Fatalf("got %+v", got)
	}
}

func TestDecodeTxError_CustomWithoutLogsPreservesProgramAndCode(t *testing.T) {
	for _, program := range []solana.PublicKey{dvpn.ProgramID, distributor.ProgramID} {
		got := core.DecodeTxError(fakeInstructionError(0, map[string]any{"Custom": 6000}), nil, []solana.PublicKey{program})
		if got == nil || got.Program != program.String() || got.Code != 6000 {
			t.Fatalf("program %s: got %+v", program, got)
		}
	}
}

func TestDecodeTxError_FailingCPI(t *testing.T) {
	outer := solana.MustPublicKeyFromBase58("11111111111111111111111111111113")
	inner := solana.MustPublicKeyFromBase58("11111111111111111111111111111114")
	logs := []string{
		"Program " + outer.String() + " invoke [1]",
		"Program " + inner.String() + " invoke [2]",
		"Program log: AnchorError occurred. Error Code: EarlierError. Error Number: 6000. Error Message: earlier.",
		"Program " + inner.String() + " success",
		"Program " + inner.String() + " invoke [2]",
		"Program log: AnchorError occurred. Error Code: ActualError. Error Number: 6001. Error Message: actual.",
		"Program " + inner.String() + " failed: custom program error: 0x1771",
		"Program " + outer.String() + " failed: custom program error: 0x1771",
	}

	got := core.DecodeTxError(fakeInstructionError(0, map[string]any{"Custom": 6001}), logs, []solana.PublicKey{outer})
	if got == nil || got.Program != inner.String() || got.Name != "ActualError" || got.Code != 6001 || got.Message != "actual." {
		t.Fatalf("got %+v", got)
	}
}

func TestDecodeTxError_FailingCPIStaticCode(t *testing.T) {
	outer := solana.MustPublicKeyFromBase58("11111111111111111111111111111113")
	logs := []string{
		"Program " + outer.String() + " invoke [1]",
		"Program " + solana.Token2022ProgramID.String() + " invoke [2]",
		"Program " + solana.Token2022ProgramID.String() + " failed: custom program error: 0x1",
		"Program " + outer.String() + " failed: custom program error: 0x1",
	}

	got := core.DecodeTxError(fakeInstructionError(0, map[string]any{"Custom": 1}), logs, []solana.PublicKey{outer})
	if got == nil || got.Error() != "token-2022 error: InsufficientFunds (1): insufficient funds" {
		t.Fatalf("got %v", got)
	}
}

func TestDecodeTxError_MalformedInstructionIndex(t *testing.T) {
	for _, index := range []any{-1, 1.5, "0", 3, 1e30} {
		err := fakeInstructionError(index, map[string]any{"Custom": 0})
		got := core.DecodeTxError(err, nil, []solana.PublicKey{solana.SystemProgramID})
		if got == nil || got.Program != "" || got.Name != "" || got.Raw != fmt.Sprint(err) {
			t.Fatalf("index %v: got %+v", index, got)
		}
	}
}

func TestDecodeTxError_RPCError(t *testing.T) {
	var result rpc.SimulateTransactionResult
	if err := json.Unmarshal([]byte(`{"err":{"InstructionError":[0,{"Custom":4}]}}`), &result); err != nil {
		t.Fatal(err)
	}

	got := core.DecodeTxError(result.Err, nil, []solana.PublicKey{solana.Token2022ProgramID})
	if got == nil || got.Error() != "token-2022 error: OwnerMismatch (4): owner does not match" {
		t.Fatalf("got %v", got)
	}
}

func fakeInstructionError(index any, inner any) any {
	return map[string]any{"InstructionError": []any{index, inner}}
}
