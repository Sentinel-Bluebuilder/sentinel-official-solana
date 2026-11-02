package main_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	main "github.com/sentinel-official/sentinel-dvpnd/cmd/sentinel-dvpnd"
	"github.com/sentinel-official/sentinel-go-sdk/v3/service"
)

func TestGroupCommands(t *testing.T) {
	t.Parallel()

	cases := []struct {
		create []string
		goos   string
		name   string
		remove []string
	}{
		{
			create: []string{"getent group sentinel-dvpn", "groupadd --system sentinel-dvpn"},
			goos:   "linux",
			name:   "groupadd and groupdel",
			remove: []string{"getent group sentinel-dvpn", "groupdel sentinel-dvpn"},
		},
		{
			create: []string{"dseditgroup -o read sentinel-dvpn", "dseditgroup -o create sentinel-dvpn"},
			goos:   "darwin",
			name:   "dseditgroup",
			remove: []string{"dseditgroup -o read sentinel-dvpn", "dseditgroup -o delete sentinel-dvpn"},
		},
		{
			create: []string{"net localgroup sentinel-dvpn", "net localgroup sentinel-dvpn /add"},
			goos:   "windows",
			name:   "net localgroup",
			remove: []string{"net localgroup sentinel-dvpn", "net localgroup sentinel-dvpn /delete"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			steps := []struct {
				act    func(context.Context, string, main.ExecFunc) error
				exists bool
				name   string
				want   []string
			}{
				{act: main.EnsureGroup, name: "ensure creates a missing group", want: tc.create},
				{act: main.EnsureGroup, exists: true, name: "ensure keeps an existing group", want: tc.create[:1]},
				{act: main.DeleteGroup, exists: true, name: "delete removes an existing group", want: tc.remove},
				{act: main.DeleteGroup, name: "delete skips a missing group", want: tc.remove[:1]},
			}
			for _, step := range steps {
				t.Run(step.name, func(t *testing.T) {
					t.Parallel()

					var got []string

					fakeExec := func(_ context.Context, c service.Command) ([]byte, error) {
						got = append(got, strings.Join(append([]string{c.Path}, c.Args...), " "))
						if len(got) == 1 && !step.exists {
							return nil, errors.New("group not found")
						}

						return nil, nil
					}

					require.NoError(t, step.act(t.Context(), tc.goos, fakeExec))
					require.Equal(t, step.want, got)
				})
			}
		})
	}
}
