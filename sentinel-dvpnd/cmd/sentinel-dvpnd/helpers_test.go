package main_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/solana-foundation/solana-go/v2"
	"github.com/stretchr/testify/require"

	main "github.com/sentinel-official/sentinel-dvpnd/cmd/sentinel-dvpnd"
	"github.com/sentinel-official/sentinel-dvpnd/internal/daemon"
	"github.com/sentinel-official/sentinel-go-sdk/v3/service"
)

const goosWindows = "windows"

type fakeRecoverRuntime struct {
	daemon.Runtime

	recoverErr error
}

func (r fakeRecoverRuntime) Recover(context.Context) error { return r.recoverErr }

type recordingExec struct {
	commands []string
}

func (r *recordingExec) run(_ context.Context, c service.Command) ([]byte, error) {
	r.commands = append(r.commands, strings.Join(append([]string{c.Path}, c.Args...), " "))

	return nil, nil
}

func randomOwner(t *testing.T) string {
	t.Helper()

	privateKey, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	return privateKey.PublicKey().String()
}

func requireFileExists(t *testing.T, path string) {
	t.Helper()

	_, err := os.Stat(path)
	require.NoError(t, err)
}

func runInitCmd(t *testing.T, home string, args ...string) (main.InitResult, error) {
	t.Helper()

	cmd := main.NewRootCmd()

	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs(append([]string{"init", "--home", home}, args...))

	if err := cmd.ExecuteContext(t.Context()); err != nil {
		return main.InitResult{}, fmt.Errorf("running init: %w", err)
	}

	var result main.InitResult
	require.NoError(t, json.NewDecoder(&output).Decode(&result))

	return result, nil
}
