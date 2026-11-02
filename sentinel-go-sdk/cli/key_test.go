package cli_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sentinel-official/sentinel-go-sdk/v3/cli"
	"github.com/sentinel-official/sentinel-go-sdk/v3/key"
)

func TestNewKeyCmd_InvalidName(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	homeDir := func() string {
		return dir
	}

	var out, errOut bytes.Buffer

	cmd := cli.NewKeyCmd(homeDir)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)

	args := []string{"new", "BAD NAME", "--unencrypted"}
	cmd.SetArgs(args)

	err := cmd.Execute()
	require.ErrorIs(t, err, cli.ErrInvalidKeyName)
}

func TestNewKeyCmd_NewListShow(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	homeDir := func() string {
		return dir
	}

	var newOut bytes.Buffer

	newCmd := cli.NewKeyCmd(homeDir)
	newCmd.SetOut(&newOut)

	newArgs := []string{"new", "operator", "--unencrypted"}
	newCmd.SetArgs(newArgs)
	require.NoError(t, newCmd.Execute())

	var created keyInfo
	require.NoError(t, json.Unmarshal(newOut.Bytes(), &created))
	require.Equal(t, "operator", created.Name)
	require.NotEmpty(t, created.PublicKey)
	require.False(t, created.Sealed)

	var listOut bytes.Buffer

	listCmd := cli.NewKeyCmd(homeDir)
	listCmd.SetOut(&listOut)

	listArgs := []string{"list"}
	listCmd.SetArgs(listArgs)
	require.NoError(t, listCmd.Execute())

	var listed []keyInfo
	require.NoError(t, json.Unmarshal(listOut.Bytes(), &listed))
	require.Len(t, listed, 1)
	require.Equal(t, created.Name, listed[0].Name)
	require.Equal(t, created.PublicKey, listed[0].PublicKey)
	require.False(t, listed[0].Sealed)

	var showOut bytes.Buffer

	showCmd := cli.NewKeyCmd(homeDir)
	showCmd.SetOut(&showOut)

	showArgs := []string{"show", "operator"}
	showCmd.SetArgs(showArgs)
	require.NoError(t, showCmd.Execute())

	var shown keyInfo
	require.NoError(t, json.Unmarshal(showOut.Bytes(), &shown))
	require.Equal(t, created.Name, shown.Name)
	require.Equal(t, created.PublicKey, shown.PublicKey)

	path := filepath.Join(dir, "operator.json")
	before, err := os.ReadFile(path) //nolint:gosec
	require.NoError(t, err)

	var errOut, discardOut bytes.Buffer

	dupCmd := cli.NewKeyCmd(homeDir)
	dupCmd.SilenceUsage = true
	dupCmd.SilenceErrors = true
	dupCmd.SetOut(&discardOut)
	dupCmd.SetErr(&errOut)

	dupArgs := []string{"new", "operator", "--unencrypted"}
	dupCmd.SetArgs(dupArgs)
	err = dupCmd.Execute()
	require.ErrorIs(t, err, key.ErrKeyExists)

	after, err := os.ReadFile(path) //nolint:gosec
	require.NoError(t, err)
	require.Equal(t, before, after)
}
