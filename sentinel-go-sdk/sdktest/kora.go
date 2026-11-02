//go:build integration

package sdktest

import (
	"context"
	"crypto/rand"
	"embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/solana-foundation/solana-go/v2"
	"github.com/stretchr/testify/require"

	"github.com/sentinel-official/sentinel-go-sdk/v3/paymaster"
	"github.com/sentinel-official/sentinel-go-sdk/v3/service"
)

const (
	koraConfFile     = "kora.toml"
	koraSignerKeyEnv = "KORA_SIGNER_PRIVATE_KEY"
	koraTemplate     = "templates/kora.toml.tmpl"
	signersConfFile  = "signers.toml"
	signersTemplate  = "templates/signers.toml.tmpl"
	stopTimeout      = 10 * time.Second
)

//go:embed templates/*.tmpl
var templates embed.FS

type Kora struct {
	APIKey string
	Signer solana.PublicKey
	URL    string
}

type koraTemplateData struct {
	APIKey       string
	Mint         solana.PublicKey
	ProgramID    solana.PublicKey
	SignerKeyEnv string
}

func StartKora(t *testing.T, localnet *Localnet) *Kora {
	t.Helper()

	binary := os.Getenv("SENTINEL_KORA_BIN")
	if binary == "" {
		skipOrFail(t, "SENTINEL_KORA_BIN not set")
	}

	binary, err := filepath.Abs(binary)
	require.NoError(t, err)

	ctx := t.Context()
	signerKey := newPrivateKey(t)

	require.NoError(t, localnet.FundSOL(ctx, signerKey.PublicKey(), airdropSOL))
	require.NoError(t, localnet.FundTokens(ctx, signerKey.PublicKey(), 0))

	dir := t.TempDir()
	data := koraTemplateData{
		APIKey:       rand.Text(),
		Mint:         localnet.Mint,
		ProgramID:    localnet.ProgramID,
		SignerKeyEnv: koraSignerKeyEnv,
	}

	require.NoError(t, service.WriteTemplate(templates, koraTemplate, filepath.Join(dir, koraConfFile), data, ""))
	require.NoError(t, service.WriteTemplate(templates, signersTemplate, filepath.Join(dir, signersConfFile), data, ""))

	kora := &Kora{
		APIKey: data.APIKey,
		Signer: signerKey.PublicKey(),
	}

	for range maxStartAttempts {
		err = startKoraProcess(t, binary, dir, localnet.RPCURL, signerKey, kora)
		if !errors.Is(err, service.ErrProcessExited) {
			break
		}
	}

	require.NoError(t, err)

	return kora
}

func startKoraProcess(t *testing.T, binary, dir, rpcURL string, signerKey solana.PrivateKey, kora *Kora) error {
	t.Helper()

	port := reservePort(t)
	command := service.Command{
		Args: []string{
			"--config", koraConfFile,
			"--rpc-url", rpcURL,
			"rpc", "start",
			"--signers-config", signersConfFile,
			"--port", strconv.Itoa(port),
		},
		Dir: dir,
		Env: []string{
			koraSignerKeyEnv + "=" + signerKey.String(),
			"KORA_API_KEY=",
			"KORA_HMAC_SECRET=",
			"KORA_RECAPTCHA_SECRET=",
		},
		Path: binary,
	}

	process, err := service.StartProcess(t.Context(), command, zerolog.New(zerolog.NewTestWriter(t)))
	if err != nil {
		return fmt.Errorf("starting kora: %w", err)
	}

	stop := func() {
		ctx, cancel := context.WithTimeout(context.Background(), stopTimeout)
		defer cancel()

		_ = process.Stop(ctx)
	}
	t.Cleanup(stop)

	kora.URL = fmt.Sprintf("http://127.0.0.1:%d", port)

	return waitPayerSigner(t.Context(), process, kora)
}

func waitPayerSigner(ctx context.Context, process service.Process, kora *Kora) error {
	ctx, cancel := context.WithTimeout(ctx, startupTimeout)
	defer cancel()

	client := paymaster.NewClient(kora.URL, kora.APIKey)

	var payerSigner paymaster.PayerSigner

	probe := func(ctx context.Context) error {
		var err error

		payerSigner, err = client.PayerSigner(ctx)

		return err
	}

	if err := service.PollReadyProcess(ctx, process, probe); err != nil {
		return fmt.Errorf("waiting for kora: %w", err)
	}

	if payerSigner.Signer != kora.Signer {
		return fmt.Errorf("kora signer %s, want %s", payerSigner.Signer, kora.Signer)
	}

	return nil
}
