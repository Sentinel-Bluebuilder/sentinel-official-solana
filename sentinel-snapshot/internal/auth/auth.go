//go:build integration

package auth

import (
	"context"
	"fmt"

	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	cryptotypes "github.com/cosmos/cosmos-sdk/crypto/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"

	"github.com/sentinel-official/sentinel-snapshot/internal/app"
)

const checkInterval = 4096

type Account struct {
	Address          sdk.AccAddress
	AddressBech32    string
	Class            Class
	CompressedPubKey []byte
}

func ClassifyAccounts(ctx context.Context, h *app.Handle, escrowAddresses map[string]bool) ([]Account, error) {
	var (
		accounts []Account
		walkErr  error
	)

	count := 0

	h.App().AccountKeeper.IterateAccounts(h.Context(), func(account authtypes.AccountI) bool {
		count++
		if count%checkInterval == 0 {
			if err := ctx.Err(); err != nil {
				walkErr = fmt.Errorf("account walk cancellation: %w", err)

				return true
			}
		}

		_, isModule := account.(authtypes.ModuleAccountI)

		pubKey := account.GetPubKey()

		var typeURL string

		if pubKey != nil {
			packed, err := packPubKeyTypeURL(account.GetAddress(), pubKey)
			if err != nil {
				walkErr = err

				return true
			}

			typeURL = packed
		}

		class := ClassifyAccount(isModule, typeURL, len(account.GetAddress()))

		if escrowAddresses[account.GetAddress().String()] {
			class = ClassTreasury
		}

		var compressed []byte

		if class == ClassLeafRevealed {
			key, err := revealedPubKey(account.GetAddress(), pubKey)
			if err != nil {
				walkErr = err

				return true
			}

			compressed = key
		}

		address := account.GetAddress()
		accounts = append(accounts, Account{
			Address:          address,
			AddressBech32:    address.String(),
			Class:            class,
			CompressedPubKey: compressed,
		})

		return false
	})

	if walkErr != nil {
		return nil, walkErr
	}

	return accounts, nil
}

func revealedPubKey(address sdk.AccAddress, pubKey cryptotypes.PubKey) ([]byte, error) {
	key, err := CompressedSecp256k1(pubKey.Bytes())
	if err != nil {
		return nil, fmt.Errorf("auth: validate revealed pub key for %s: %w", address, err)
	}

	return key, nil
}

func packPubKeyTypeURL(address sdk.AccAddress, pubKey cryptotypes.PubKey) (string, error) {
	packed, err := codectypes.NewAnyWithValue(pubKey)
	if err != nil {
		return "", fmt.Errorf("auth: pack pub key for %s: %w", address, err)
	}

	return packed.TypeUrl, nil
}
