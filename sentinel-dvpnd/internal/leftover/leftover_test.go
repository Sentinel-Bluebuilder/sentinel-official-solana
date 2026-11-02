package leftover_test

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sentinel-official/sentinel-dvpnd/internal/leftover"
	"github.com/sentinel-official/sentinel-go-sdk/v3/service"
)

func TestRemoveHint(t *testing.T) {
	t.Parallel()

	const dir = "/etc/wireguard/sentinel-dvpn"

	failure := func(path string) error {
		return fmt.Errorf("removing %s: %w", path, &fs.PathError{Err: fs.ErrPermission, Op: "remove", Path: path})
	}

	cases := []struct {
		err  error
		name string
		want string
	}{
		{
			err:  fmt.Errorf("reading %s: %w", dir, &fs.PathError{Err: fs.ErrPermission, Op: "open", Path: dir}),
			name: "unreadable dir",
		},
		{
			err:  errors.Join(failure(dir + "/dvpn-n0.conf")),
			name: "one copy",
			want: `; remove the failed copies with "rm ` + dir + `/dvpn-n0.conf"`,
		},
		{
			err:  errors.Join(failure(dir+"/dvpn-n0.conf"), failure(dir+"/dvpn-n0.json")),
			name: "several copies",
			want: `; remove the failed copies with "rm ` + dir + `/dvpn-n0.conf ` + dir + `/dvpn-n0.json"`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tc.want, leftover.RemoveHint(dir, tc.err))
		})
	}
}

func TestRemover_RemoveInterface(t *testing.T) {
	t.Parallel()

	deleteErr := errors.New("exit status 2")
	deleted := []string{"ip link delete dev dvpn-n0"}

	cases := []struct {
		commands []string
		err      error
		goos     string
		name     string
		present  []bool
		runErr   error
	}{
		{goos: "linux", name: "absent", present: []bool{false}},
		{commands: deleted, goos: "linux", name: "deleted", present: []bool{true}},
		{
			commands: deleted, err: leftover.ErrInterfaceExists, goos: "linux", name: "still present",
			present: []bool{true, true}, runErr: deleteErr,
		},
		{commands: deleted, goos: "linux", name: "gone on recheck", present: []bool{true, false}, runErr: deleteErr},
		{goos: "windows", name: "windows"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var commands []string

			lookups := 0
			exists := func(name string) (bool, error) {
				lookups++

				return tc.present[lookups-1] && name == "dvpn-n0", nil
			}
			exec := func(_ context.Context, command service.Command) ([]byte, error) {
				commands = append(commands, strings.Join(append([]string{command.Path}, command.Args...), " "))

				return nil, tc.runErr
			}

			err := leftover.NewRemoverForTest(exists, tc.goos, exec).RemoveInterface(t.Context(), "dvpn-n0")

			require.ErrorIs(t, err, tc.err)
			require.Equal(t, tc.commands, commands)
			require.Len(t, tc.present, lookups)

			if tc.err != nil {
				require.ErrorContains(t, err, `dvpn-n0; remove it with "ip link delete dev dvpn-n0"`)
			}
		})
	}
}
