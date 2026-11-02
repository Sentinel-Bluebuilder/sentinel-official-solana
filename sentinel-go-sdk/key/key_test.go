package key_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/solana-foundation/solana-go/v2"
	"github.com/stretchr/testify/require"

	"github.com/sentinel-official/sentinel-go-sdk/v3/key"
)

func TestCreate_RefusesExisting(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "key.json")

	privateKey, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	require.NoError(t, key.Create(path, privateKey, ""))

	got, err := key.Load(path, "")
	require.NoError(t, err)
	require.Equal(t, privateKey, got)

	other, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	err = key.Create(path, other, "")
	require.ErrorIs(t, err, key.ErrKeyExists)

	reloaded, err := key.Load(path, "")
	require.NoError(t, err)
	require.Equal(t, privateKey, reloaded)
}

func TestSave(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		passphrase string
	}{
		{name: "unencrypted", passphrase: ""},
		{name: "sealed", passphrase: "test-passphrase"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(t.TempDir(), "key.json")
			privateKey, err := solana.NewRandomPrivateKey()
			require.NoError(t, err)

			require.NoError(t, key.Save(path, privateKey, tc.passphrase))

			got, err := key.Load(path, tc.passphrase)
			require.NoError(t, err)
			require.Equal(t, privateKey, got)

			if tc.passphrase == "" {
				info, err := os.Stat(path)
				require.NoError(t, err)
				require.Equal(t, os.FileMode(0o600), info.Mode().Perm())

				upstream, err := solana.PrivateKeyFromSolanaKeygenFile(path)
				require.NoError(t, err)
				require.Equal(t, privateKey, upstream)

				return
			}

			_, err = key.Load(path, "")
			require.ErrorIs(t, err, key.ErrKeyLocked)

			_, err = key.Load(path, "wrong-passphrase")
			require.ErrorIs(t, err, key.ErrInvalidPassphrase)
		})
	}
}
