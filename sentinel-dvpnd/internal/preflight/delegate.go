package preflight

import (
	"context"
	"fmt"

	bin "github.com/gagliardetto/binary"
	"github.com/solana-foundation/solana-go/v2"
	"github.com/solana-foundation/solana-go/v2/programs/token"
	"github.com/solana-foundation/solana-go/v2/rpc"

	"github.com/sentinel-official/sentinel-dvpnd/internal/config"
	"github.com/sentinel-official/sentinel-go-sdk/v3/program"
	"github.com/sentinel-official/sentinel-go-sdk/v3/service"
	"github.com/sentinel-official/sentinel-programs/clients/go/dvpn"
)

func checkDelegate(ctx context.Context, loaded *config.Loaded, programClient *program.Client) service.PreflightCheck {
	const remedy = "run sentinel-dvpn wallet approve-delegate --amount-base-units unlimited with the provider owner key"

	owner, err := solana.PublicKeyFromBase58(loaded.Provider.Owner)
	if err != nil {
		return newWarning("delegate", fmt.Sprintf("invalid provider owner: %v; %s", err, remedy))
	}

	address, err := programClient.AssociatedTokenAddress(owner)
	if err != nil {
		return newWarning("delegate", fmt.Sprintf("deriving owner token account: %v; %s", err, remedy))
	}

	resp, err := programClient.RPC.GetAccountInfoWithOpts(ctx, address, &rpc.GetAccountInfoOpts{
		Commitment: programClient.Commitment,
		Encoding:   solana.EncodingBase64,
	})
	if err != nil {
		return newWarning("delegate", fmt.Sprintf("reading owner token account: %v; %s", err, remedy))
	}

	if resp.Value.Owner != solana.Token2022ProgramID {
		return newWarning("delegate", "owner token account is not owned by Token-2022; "+remedy)
	}

	var account token.Account
	if err := bin.NewBinDecoder(resp.GetBinary()).Decode(&account); err != nil {
		return newWarning("delegate", fmt.Sprintf("decoding owner token account: %v; %s", err, remedy))
	}

	delegate, _, err := dvpn.FindConfigAddress(programClient.ProgramID)
	if err != nil {
		return newWarning("delegate", fmt.Sprintf("deriving delegate address: %v; %s", err, remedy))
	}

	if account.Delegate == nil || *account.Delegate != delegate || account.DelegatedAmount == 0 {
		return newWarning("delegate", "provider delegate approval is missing or exhausted; "+remedy)
	}

	return newCheck("delegate", nil)
}
