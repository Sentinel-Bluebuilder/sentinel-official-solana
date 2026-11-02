package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/solana-foundation/solana-go/v2"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/sentinel-official/sentinel-go-sdk/v3/key"
)

var (
	ErrInvalidKeyName     = errors.New("key name must match ^[a-z0-9][a-z0-9._-]{0,63}$")
	ErrPassphraseMismatch = errors.New("passphrases do not match")
)

var keyNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

type keyInfo struct {
	Name      string `json:"name"`
	PublicKey string `json:"public_key"`
	Sealed    bool   `json:"sealed"`
}

func KeyPath(dir, name string) (string, error) {
	if !keyNamePattern.MatchString(name) {
		return "", fmt.Errorf("%w: %q", ErrInvalidKeyName, name)
	}

	return filepath.Join(dir, name+".json"), nil
}

func NewKeyCmd(dir func() string) *cobra.Command {
	cmd := &cobra.Command{
		Short: "manage key files",
		Use:   "key",
	}
	cmd.AddCommand(newKeyNewCmd(dir), newKeyShowCmd(dir), newKeyListCmd(dir))

	return cmd
}

func ReadPassphrase(prompt string, confirm bool) (string, error) {
	passphrase, err := readSecret(prompt)
	if err != nil {
		return "", err
	}

	if confirm {
		again, err := readSecret("confirm " + prompt)
		if err != nil {
			return "", err
		}

		if passphrase != again {
			return "", ErrPassphraseMismatch
		}
	}

	return passphrase, nil
}

func newKeyListCmd(dir func() string) *cobra.Command {
	run := func(cmd *cobra.Command, _ []string) error {
		entries, err := os.ReadDir(dir())
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("reading key directory: %w", err)
		}

		infos := []keyInfo{}

		for _, entry := range entries {
			name := strings.TrimSuffix(entry.Name(), ".json")
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") || !keyNamePattern.MatchString(name) {
				continue
			}

			raw, err := os.ReadFile(filepath.Join(dir(), entry.Name()))
			if err != nil {
				return fmt.Errorf("reading key file %s: %w", entry.Name(), err)
			}

			info := keyInfo{
				Name:   name,
				Sealed: strings.HasPrefix(strings.TrimSpace(string(raw)), "{"),
			}
			if !info.Sealed {
				privateKey, err := key.Load(filepath.Join(dir(), entry.Name()), "")
				if err != nil {
					return fmt.Errorf("loading key %s: %w", entry.Name(), err)
				}

				info.PublicKey = privateKey.PublicKey().String()
			}

			infos = append(infos, info)
		}

		return json.NewEncoder(cmd.OutOrStdout()).Encode(infos)
	}

	cmd := &cobra.Command{
		Args:  cobra.NoArgs,
		RunE:  run,
		Short: "list key files",
		Use:   "list",
	}

	return cmd
}

func newKeyNewCmd(dir func() string) *cobra.Command {
	var unencrypted bool

	run := func(cmd *cobra.Command, args []string) error {
		path, err := KeyPath(dir(), args[0])
		if err != nil {
			return err
		}

		privateKey, err := solana.NewRandomPrivateKey()
		if err != nil {
			return fmt.Errorf("generating key: %w", err)
		}

		passphrase := ""
		if !unencrypted {
			passphrase, err = ReadPassphrase("passphrase", true)
			if err != nil {
				return err
			}
		}

		if err := key.Create(path, privateKey, passphrase); err != nil {
			return fmt.Errorf("creating key file: %w", err)
		}

		info := keyInfo{
			Name:      args[0],
			PublicKey: privateKey.PublicKey().String(),
			Sealed:    passphrase != "",
		}

		return json.NewEncoder(cmd.OutOrStdout()).Encode(info)
	}

	cmd := &cobra.Command{
		Args:  cobra.ExactArgs(1),
		RunE:  run,
		Short: "create a key file",
		Use:   "new <name>",
	}
	cmd.Flags().BoolVar(&unencrypted, "unencrypted", false, "write an unencrypted solana-keygen file")

	return cmd
}

func newKeyShowCmd(dir func() string) *cobra.Command {
	run := func(cmd *cobra.Command, args []string) error {
		path, err := KeyPath(dir(), args[0])
		if err != nil {
			return err
		}

		privateKey, err := key.Load(path, "")
		if errors.Is(err, key.ErrKeyLocked) {
			passphrase, perr := ReadPassphrase("passphrase", false)
			if perr != nil {
				return perr
			}

			privateKey, err = key.Load(path, passphrase)
		}

		if err != nil {
			return fmt.Errorf("loading key: %w", err)
		}

		info := keyInfo{
			Name:      args[0],
			PublicKey: privateKey.PublicKey().String(),
		}

		return json.NewEncoder(cmd.OutOrStdout()).Encode(info)
	}

	cmd := &cobra.Command{
		Args:  cobra.ExactArgs(1),
		RunE:  run,
		Short: "print a key's public key",
		Use:   "show <name>",
	}

	return cmd
}

func readSecret(prompt string) (string, error) {
	fmt.Fprintf(os.Stderr, "%s: ", prompt)

	line, err := term.ReadPassword(int(os.Stdin.Fd()))

	fmt.Fprintln(os.Stderr)

	if err != nil {
		return "", fmt.Errorf("reading passphrase: %w", err)
	}

	return string(line), nil
}
