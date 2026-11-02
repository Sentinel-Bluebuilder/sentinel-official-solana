package auth_test

import (
	"context"
	"slices"

	"github.com/solana-foundation/solana-go/v2"

	"github.com/sentinel-official/sentinel-go-sdk/v3/auth"
)

type fakeGrant struct {
	permission uint64
	subjects   []solana.PublicKey
}

type fakeGrantSource map[solana.PublicKey]fakeGrant

func (m fakeGrantSource) HasGrant(
	_ context.Context,
	_, signer solana.PublicKey,
	permission uint64,
	subjects ...solana.PublicKey,
) (bool, error) {
	grant, ok := m[signer]

	return ok && grant.permission == permission && slices.Equal(grant.subjects, subjects), nil
}

type metadataFixture struct {
	ExpiresAt int64            `json:"expires_at"`
	IssuedAt  int64            `json:"issued_at"`
	Operator  solana.PublicKey `json:"operator"`
	Owner     solana.PublicKey `json:"owner"`
	ProgramID solana.PublicKey `json:"program_id"`
	Subject   solana.PublicKey `json:"subject"`
	Version   uint8            `json:"version"`
}

type payload struct {
	Name  string `json:"name"`
	Value uint64 `json:"value"`
}

func claimsFixture(fixture metadataFixture) auth.MetadataClaims {
	return auth.MetadataClaims{
		ExpiresUnix: fixture.ExpiresAt,
		IssuedUnix:  fixture.IssuedAt,
		Operator:    fixture.Operator,
		Owner:       fixture.Owner,
		ProgramID:   fixture.ProgramID,
		Subject:     fixture.Subject,
		Version:     fixture.Version,
	}
}
