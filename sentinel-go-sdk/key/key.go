package key

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/solana-foundation/solana-go/v2"
	"github.com/solana-foundation/solana-go/v2/vault"
)

var (
	ErrInvalidPassphrase = errors.New("invalid passphrase")
	ErrKeyExists         = errors.New("key file already exists")
	ErrKeyFileEmpty      = errors.New("key file holds no key")
	ErrKeyLocked         = errors.New("key file is sealed and no passphrase was given")
)

func Create(path string, privateKey solana.PrivateKey, passphrase string) error {
	content, err := encode(privateKey, passphrase)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("creating key directory: %w", err)
	}

	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) //nolint:gosec
	if errors.Is(err, os.ErrExist) {
		return fmt.Errorf("%w: %s", ErrKeyExists, path)
	}

	if err != nil {
		return fmt.Errorf("creating key file: %w", err)
	}

	if _, err := file.Write(content); err != nil {
		_ = file.Close()
		_ = os.Remove(path)

		return fmt.Errorf("writing key file: %w", err)
	}

	if err := file.Close(); err != nil {
		_ = os.Remove(path)

		return fmt.Errorf("closing key file: %w", err)
	}

	return nil
}

func Load(path, passphrase string) (solana.PrivateKey, error) {
	raw, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		return nil, fmt.Errorf("reading key file: %w", err)
	}

	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) > 0 && trimmed[0] == '[' {
		privateKey, err := solana.PrivateKeyFromSolanaKeygenFileBytes(raw)
		if err != nil {
			return nil, fmt.Errorf("parsing key file: %w", err)
		}

		return privateKey, nil
	}

	if passphrase == "" {
		return nil, ErrKeyLocked
	}

	wallet, err := vault.NewVaultFromWalletFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading vault: %w", err)
	}

	if err := wallet.Open(vault.NewPassphraseBoxer(passphrase)); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidPassphrase, err)
	}

	if len(wallet.KeyBag) == 0 {
		return nil, ErrKeyFileEmpty
	}

	return wallet.KeyBag[0], nil
}

func Save(path string, privateKey solana.PrivateKey, passphrase string) error {
	content, err := encode(privateKey, passphrase)
	if err != nil {
		return err
	}

	return writeAtomic(path, content)
}

func encode(privateKey solana.PrivateKey, passphrase string) ([]byte, error) {
	if passphrase == "" {
		raw, err := solanaKeygenJSON(privateKey)
		if err != nil {
			return nil, fmt.Errorf("encoding key: %w", err)
		}

		return []byte(raw), nil
	}

	wallet := vault.NewVault()
	wallet.AddPrivateKey(privateKey)

	if err := wallet.Seal(vault.NewPassphraseBoxer(passphrase)); err != nil {
		return nil, fmt.Errorf("sealing vault: %w", err)
	}

	content, err := json.MarshalIndent(wallet, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encoding vault: %w", err)
	}

	return content, nil
}

func solanaKeygenJSON(privateKey solana.PrivateKey) (string, error) {
	ints := make([]int, len(privateKey))
	for i, octet := range []byte(privateKey) {
		ints[i] = int(octet)
	}

	raw, err := json.Marshal(ints)
	if err != nil {
		return "", fmt.Errorf("marshaling key bytes: %w", err)
	}

	return string(raw), nil
}

func writeAtomic(path string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("creating key directory: %w", err)
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}

	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())

		return fmt.Errorf("setting key file mode: %w", err)
	}

	if _, err := tmp.Write(content); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())

		return fmt.Errorf("writing key file: %w", err)
	}

	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())

		return fmt.Errorf("closing key file: %w", err)
	}

	if err := os.Rename(tmp.Name(), path); err != nil {
		_ = os.Remove(tmp.Name())

		return fmt.Errorf("renaming key file: %w", err)
	}

	return nil
}
