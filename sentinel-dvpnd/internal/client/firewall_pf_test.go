package client_test

import (
	"context"
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sentinel-official/sentinel-dvpnd/internal/client"
)

const (
	pfAnchorFlushCommand = "pfctl -a sentinel-dvpn-c0 -F all"
	pfAnchorLoadCommand  = "pfctl -a sentinel-dvpn-c0 -f -"
	pfAnchorRulesCommand = "pfctl -a sentinel-dvpn-c0 -sr"
	pfCleanInterfaces    = "lo0\nen0\n"
	pfInterfacesCommand  = "pfctl -v -s Interfaces"
	pfMainRules          = "scrub-anchor \"com.apple/*\" all fragment reassemble\n" +
		"anchor \"com.apple/*\" all\n"
	pfReferences42 = "TOKENS:\nPID Process Name TOKEN TIMESTAMP\n100 pfctl 42 0 days 00:00:01\n"
	pfRootAnchor   = "anchor \"sentinel-dvpn-c0\" out quick all\n"
	pfRootCommand  = "pfctl -sr"
	pfTestAnchor   = "sentinel-dvpn-c0"
)

func TestParsePFToken(t *testing.T) {
	t.Parallel()

	for _, out := range []string{"Token : 1234567890\n", "pf enabled\nToken : 1234567890\n"} {
		token, err := client.ParsePFToken([]byte(out))
		require.NoError(t, err)
		require.Equal(t, "1234567890", token)
	}

	for _, out := range []string{"pf enabled\n", "Token:", "Token: 0", "Token: +42", "OtherToken: 42",
		"Token: 18446744073709551616", "Token: 42\nToken: 43"} {
		_, err := client.ParsePFToken([]byte(out))
		require.Error(t, err)
	}
}

func TestPFFirewall_Apply_AnchorPrecedence(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		root string
	}{
		{name: "bare anchor", root: `anchor "sentinel-dvpn-c0" all`},
		{name: "anchor after pass", root: "pass out quick all\n" + pfRootAnchor},
		{name: "duplicate anchor", root: pfRootAnchor + pfRootAnchor},
		{name: "anchor on interface", root: `anchor "sentinel-dvpn-c0" out quick on en0 all`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			run := &fakeRun{outputs: map[string]string{pfInterfacesCommand: pfCleanInterfaces, pfRootCommand: tc.root}}
			fw := client.NewPFFirewall(pfTestAnchor, filepath.Join(t.TempDir(), "pf.token"), run.run)
			require.Error(t, fw.Apply(t.Context(), "", nil, nil, false))
			require.Equal(t, []string{pfInterfacesCommand, pfRootCommand}, run.commands())
		})
	}

	for _, tc := range []struct {
		name string
		root string
	}{
		{name: "commented anchor", root: "# " + pfRootAnchor + "pass out quick all\n"},
		{name: "other anchor", root: "anchor \"sentinel-dvpn-c1\" all\npass out all\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			run := &fakeRun{outputs: map[string]string{
				pfInterfacesCommand: pfCleanInterfaces,
				pfRootCommand:       tc.root,
				"pfctl -E":          "Token : 42",
			}}
			fw := client.NewPFFirewall(pfTestAnchor, filepath.Join(t.TempDir(), "pf.token"), run.run)
			require.NoError(t, fw.Apply(t.Context(), "", nil, nil, false))
			require.Contains(t, run.calls[2].input, tc.root[strings.Index(tc.root, "\n")+1:])
			require.Contains(t, run.calls[2].input, pfRootAnchor)
		})
	}
}

func TestPFFirewall_Apply_AnchorPresent(t *testing.T) {
	t.Parallel()

	tokenPath := filepath.Join(t.TempDir(), "pf.token")
	require.NoError(t, os.WriteFile(tokenPath, []byte("42"), 0o600))

	run := &fakeRun{outputs: map[string]string{
		pfInterfacesCommand:   pfCleanInterfaces,
		"pfctl -s References": pfReferences42,
		pfRootCommand:         pfRootAnchor + pfMainRules,
	}}
	fw := client.NewPFFirewall(pfTestAnchor, tokenPath, run.run)

	require.NoError(t, fw.Apply(t.Context(), "utun3", nil, nil, false))

	want := []string{pfInterfacesCommand, pfRootCommand, pfAnchorLoadCommand, "pfctl -s References", "pfctl -F states"}
	require.Equal(t, want, run.commands())
}

func TestPFFirewall_Apply_AttachesAnchorAndEnables(t *testing.T) {
	t.Parallel()

	tokenPath := filepath.Join(t.TempDir(), "pf.token")
	run := &fakeRun{outputs: map[string]string{pfInterfacesCommand: pfCleanInterfaces,
		pfRootCommand: pfMainRules,
		"pfctl -E":    "pf enabled\nToken : 42\n",
	}}
	fw := client.NewPFFirewall(pfTestAnchor, tokenPath, run.run)

	nodeAddrs := []netip.Addr{netip.MustParseAddr("203.0.113.1")}
	require.NoError(t, fw.Apply(t.Context(), "utun3", nodeAddrs, nil, false))

	want := []string{
		pfInterfacesCommand,
		pfRootCommand,
		"pfctl -R -f -",
		pfAnchorLoadCommand,
		"pfctl -E",
		"pfctl -F states",
	}
	require.Equal(t, want, run.commands())
	require.Equal(t, "scrub-anchor \"com.apple/*\" all fragment reassemble\n"+
		pfRootAnchor+"anchor \"com.apple/*\" all\n", run.calls[2].input)
	require.Equal(t, client.RenderPFRuleset("utun3", nodeAddrs, nil, false), run.calls[3].input)

	token, err := os.ReadFile(tokenPath) //#nosec G304
	require.NoError(t, err)
	require.Equal(t, "42", string(token))
}

func TestPFFirewall_Apply_CommandErrors(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		command string
	}{
		{command: pfInterfacesCommand, name: "list interfaces"},
		{command: pfRootCommand, name: "show root rules"},
		{command: "pfctl -R -f -", name: "load root rules"},
		{command: pfAnchorLoadCommand, name: "load anchor rules"},
		{command: "pfctl -E", name: "enable"},
		{command: "pfctl -F states", name: "flush states"},
		{command: pfAnchorFlushCommand, name: "flush anchor"},
		{command: "pfctl -s info", name: "show info"},
		{command: pfAnchorRulesCommand, name: "show anchor rules"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			failure := errors.New("command failed")
			run := &fakeRun{errs: map[string]error{tc.command: failure},
				outputs: map[string]string{
					pfInterfacesCommand: pfCleanInterfaces,
					"pfctl -E":          "Token : 42", "pfctl -s info": "Status: Enabled",
				}}
			fw := client.NewPFFirewall(pfTestAnchor, filepath.Join(t.TempDir(), "pf.token"), run.run)

			switch tc.command {
			case pfAnchorFlushCommand:
				require.ErrorIs(t, fw.Flush(t.Context()), failure)

			case "pfctl -s info", pfAnchorRulesCommand:
				run.outputs[pfRootCommand] = pfRootAnchor
				_, err := client.PFLoaded(t.Context(), run.run, pfTestAnchor)
				require.ErrorIs(t, err, failure)

			default:
				err := fw.Apply(t.Context(), "", nil, nil, false)
				require.ErrorIs(t, err, failure)

				changed := tc.command == pfAnchorLoadCommand || tc.command == "pfctl -E" || tc.command == "pfctl -F states"
				require.Equal(t, changed, errors.Is(err, client.ErrFirewallChanged))
			}
		})
	}
}

func TestPFFirewall_Apply_InterfaceInspection(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		err  error
		name string
		out  string
		safe bool
	}{
		{name: "clean", out: pfCleanInterfaces, safe: true},
		{name: "loopback", out: "lo0 (skip)\nen0\n", safe: true},
		{name: "physical skip", out: "lo0\nen0 (skip)\n"},
		{name: "malformed", out: "lo0\nen0 (unknown)\n"},
		{name: "empty"},
		{name: "unknown", out: "unknown"},
		{name: "duplicate", out: "lo0\nen0\nlo0\n"},
		{name: "altq diagnostic", out: "No ALTQ support in kernel\nALTQ related functions disabled\nlo0\nen0", safe: true},
		{err: errors.New("inspection failed"), name: "failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			run := &fakeRun{outputs: map[string]string{
				pfInterfacesCommand:  tc.out,
				"pfctl -E":           "Token : 42",
				"pfctl -s info":      "Status: Enabled",
				pfRootCommand:        pfRootAnchor,
				pfAnchorRulesCommand: "block drop out all\n",
			}, errs: map[string]error{pfInterfacesCommand: tc.err}}
			fw := client.NewPFFirewall(pfTestAnchor, filepath.Join(t.TempDir(), "pf.token"), run.run)

			err := fw.Apply(t.Context(), "utun3", nil, nil, false)
			if tc.safe {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
				require.Equal(t, []string{pfInterfacesCommand}, run.commands())
			}

			loaded, err := client.PFLoaded(t.Context(), run.run, pfTestAnchor)
			require.Equal(t, tc.safe, loaded)

			if tc.safe {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}

func TestPFFirewall_Apply_PublicationFailure(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "pf.token")
	run := &fakePFRollback{onEnable: func() { require.NoError(t, os.Mkdir(path, 0o700)) }}
	fw := client.NewPFFirewall(pfTestAnchor, path, run.run)
	require.Error(t, fw.Apply(t.Context(), "", nil, nil, false))
	require.True(t, run.released)
	require.True(t, run.bounded)

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.True(t, entries[0].IsDir())
	require.NoError(t, os.Remove(path))

	run.onEnable = nil

	require.NoError(t, fw.Apply(t.Context(), "", nil, nil, false))

	raw, err := os.ReadFile(path) //#nosec G304
	require.NoError(t, err)
	require.Equal(t, "42", string(raw))
}

func TestPFFirewall_Apply_ReconcilesReference(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		out     string
		acquire bool
	}{
		{name: "live", out: pfReferences42},
		{acquire: true, name: "reboot disabled", out: "No pf starter references held"},
		{name: "another owner enabled",
			out:     "TOKENS:\nPID Process Name TOKEN TIMESTAMP\n100 other 77 0 days 00:00:01",
			acquire: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(t.TempDir(), "pf.token")
			require.NoError(t, os.WriteFile(path, []byte("42"), 0o600))

			run := &fakeRun{outputs: map[string]string{
				pfInterfacesCommand: pfCleanInterfaces, "pfctl -s References": tc.out, "pfctl -E": "Token : 99",
			}}

			fw := client.NewPFFirewall(pfTestAnchor, path, run.run)
			require.NoError(t, fw.Apply(t.Context(), "", nil, nil, false))

			raw, err := os.ReadFile(path) //#nosec G304
			require.NoError(t, err)

			if tc.acquire {
				require.Equal(t, "99", string(raw))
				require.Contains(t, run.commands(), "pfctl -E")
			} else {
				require.Equal(t, "42", string(raw))
				require.NotContains(t, run.commands(), "pfctl -E")
			}
		})
	}
}

func TestPFFirewall_Apply_ReferenceInspectionFailure(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		out  string
	}{
		{name: "empty", out: ""},
		{name: "header only", out: "TOKENS:"},
		{name: "command error", out: "command error"},
		{name: "invalid token row", out: "TOKENS:\nPID Process Name TOKEN TIMESTAMP\n100 pfctl invalid 0 days 00:00:01"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(t.TempDir(), "pf.token")
			require.NoError(t, os.WriteFile(path, []byte("42"), 0o600))

			run := &fakeRun{outputs: map[string]string{
				pfInterfacesCommand: pfCleanInterfaces, "pfctl -s References": tc.out,
			}}
			if tc.out == "command error" {
				run.errs = map[string]error{"pfctl -s References": errors.New("inspection failed")}
			}

			fw := client.NewPFFirewall(pfTestAnchor, path, run.run)
			require.Error(t, fw.Apply(t.Context(), "", nil, nil, false))
			require.Error(t, fw.Flush(t.Context()))
			require.NotContains(t, run.commands(), "pfctl -E")
			require.NotContains(t, run.commands(), "pfctl -X 42")

			raw, err := os.ReadFile(path) //#nosec G304
			require.NoError(t, err)
			require.Equal(t, "42", string(raw))
		})
	}
}

func TestPFFirewall_Apply_RejectsTokenSymlink(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path, target := filepath.Join(dir, "pf.token"), filepath.Join(dir, "target")
	require.NoError(t, os.Symlink(target, path))

	run := &fakeRun{outputs: map[string]string{pfInterfacesCommand: pfCleanInterfaces}}
	fw := client.NewPFFirewall(pfTestAnchor, path, run.run)
	require.Error(t, fw.Apply(t.Context(), "", nil, nil, false))
	require.NotContains(t, run.commands(), "pfctl -E")

	_, err := os.Stat(target)
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestPFFirewall_Apply_TokenReadError(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		value string
	}{
		{name: "empty", value: ""},
		{name: "not a number", value: "invalid"},
		{name: "zero", value: "0"},
		{name: "plus sign", value: "+42"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(t.TempDir(), "pf.token")
			require.NoError(t, os.WriteFile(path, []byte(tc.value), 0o600))

			run := &fakeRun{outputs: map[string]string{
				pfInterfacesCommand: pfCleanInterfaces,
				pfRootCommand:       pfRootAnchor,
			}}
			fw := client.NewPFFirewall(pfTestAnchor, path, run.run)
			require.Error(t, fw.Apply(t.Context(), "", nil, nil, false))
			require.NotContains(t, run.commands(), "pfctl -E")
			require.Error(t, fw.Flush(t.Context()))
			require.NotContains(t, strings.Join(run.commands(), "\n"), "pfctl -X")
		})
	}

	run := &fakeRun{outputs: map[string]string{
		pfInterfacesCommand:   pfCleanInterfaces,
		"pfctl -s References": pfReferences42,
	}}
	fw := client.NewPFFirewall(pfTestAnchor, t.TempDir(), run.run)
	require.Error(t, fw.Apply(t.Context(), "", nil, nil, false))
	require.NotContains(t, run.commands(), "pfctl -E")
	require.Error(t, fw.Flush(t.Context()))
}

func TestPFFirewall_Apply_TokenSaveRollback(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	rollbackFailure := errors.New("rollback failed")
	run := &fakePFRollback{cancel: cancel, err: rollbackFailure}

	fw := client.NewPFFirewall(pfTestAnchor, filepath.Join(t.TempDir(), "missing", "pf.token"), run.run)
	err := fw.Apply(ctx, "", nil, nil, false)
	require.ErrorIs(t, err, os.ErrNotExist)
	require.ErrorIs(t, err, rollbackFailure)
	require.True(t, run.released)
	require.True(t, run.bounded)
	require.NoError(t, run.contextErr)
}

func TestPFFirewall_Flush(t *testing.T) {
	t.Parallel()

	tokenPath := filepath.Join(t.TempDir(), "pf.token")
	require.NoError(t, os.WriteFile(tokenPath, []byte("42"), 0o600))

	run := &fakeRun{outputs: map[string]string{
		pfInterfacesCommand:   pfCleanInterfaces,
		"pfctl -s References": pfReferences42,
	}}
	fw := client.NewPFFirewall(pfTestAnchor, tokenPath, run.run)

	require.NoError(t, fw.Flush(t.Context()))
	require.Equal(t, []string{pfAnchorFlushCommand, "pfctl -s References", "pfctl -X 42"}, run.commands())

	_, err := os.Stat(tokenPath)
	require.ErrorIs(t, err, os.ErrNotExist)

	run.calls = nil

	require.NoError(t, fw.Flush(t.Context()))
	require.Equal(t, []string{pfAnchorFlushCommand}, run.commands())
}

func TestPFFirewall_Flush_AbsentReferenceCleanup(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "pf.token")
	require.NoError(t, os.WriteFile(path, []byte("42"), 0o600))

	run := &fakeRun{outputs: map[string]string{"pfctl -s References": "No pf starter references held"}}
	fw := client.NewPFFirewall(pfTestAnchor, path, run.run)
	require.NoError(t, fw.Flush(t.Context()))
	require.NotContains(t, run.commands(), "pfctl -X 42")

	_, err := os.Stat(path)
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestPFFirewall_Flush_TokenRetry(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "pf.token")
	require.NoError(t, os.WriteFile(path, []byte("42"), 0o600))

	failure := errors.New("release failed")
	run := &fakeRun{errs: map[string]error{"pfctl -X 42": failure},
		outputs: map[string]string{
			"pfctl -s References": pfReferences42,
		}}
	fw := client.NewPFFirewall(pfTestAnchor, path, run.run)
	require.ErrorIs(t, fw.Flush(t.Context()), failure)

	raw, err := os.ReadFile(path) //#nosec G304
	require.NoError(t, err)
	require.Equal(t, "42", string(raw))

	delete(run.errs, "pfctl -X 42")
	require.NoError(t, fw.Flush(t.Context()))

	_, err = os.Stat(path)
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestPFLoaded(t *testing.T) {
	t.Parallel()

	run := &fakeRun{outputs: map[string]string{pfInterfacesCommand: pfCleanInterfaces,
		pfRootCommand:        pfRootAnchor,
		"pfctl -s info":      "Status: Enabled for 0 days 00:01:00           Debug: Urgent\n",
		pfAnchorRulesCommand: "block drop out all\n",
	}}

	loaded, err := client.PFLoaded(t.Context(), run.run, pfTestAnchor)
	require.NoError(t, err)
	require.True(t, loaded)

	run.outputs[pfAnchorRulesCommand] = "\n"
	loaded, err = client.PFLoaded(t.Context(), run.run, pfTestAnchor)
	require.NoError(t, err)
	require.False(t, loaded)

	run.outputs["pfctl -s info"] = "Status: Disabled\n"
	run.outputs[pfAnchorRulesCommand] = "block drop out all\n"
	loaded, err = client.PFLoaded(t.Context(), run.run, pfTestAnchor)
	require.NoError(t, err)
	require.False(t, loaded)
}

func TestPFLoaded_RequiresReachableAnchor(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		root string
	}{
		{name: "empty", root: ""},
		{name: "commented anchor", root: "# " + pfRootAnchor},
		{name: "other anchor", root: `anchor "sentinel-dvpn-c1" out quick all`},
		{name: "anchor after pass", root: "pass out quick all\n" + pfRootAnchor},
		{name: "bare anchor", root: `anchor "sentinel-dvpn-c0" all`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			run := &fakeRun{outputs: map[string]string{pfInterfacesCommand: pfCleanInterfaces,
				"pfctl -s info": "Status: Enabled", pfRootCommand: tc.root, pfAnchorRulesCommand: "block drop out all\n",
			}}
			loaded, err := client.PFLoaded(t.Context(), run.run, pfTestAnchor)
			require.NoError(t, err)
			require.False(t, loaded)
		})
	}
}

func TestRenderPFRuleset(t *testing.T) {
	t.Parallel()

	nodeAddrs := []netip.Addr{netip.MustParseAddr("203.0.113.1"), netip.MustParseAddr("2001:db8::9")}
	rpcAndWSAddrs := []netip.AddrPort{
		netip.MustParseAddrPort("10.99.0.1:8899"),
		netip.MustParseAddrPort("[2001:db8::7]:443"),
	}

	want := "block drop out all\n" +
		"pass out quick on lo0 all\n" +
		"pass out quick on utun3 all\n" +
		"pass out quick to 203.0.113.1\n" +
		"pass out quick to 2001:db8::9\n" +
		"pass out quick proto tcp to 10.99.0.1 port 8899\n" +
		"pass out quick proto tcp to 2001:db8::7 port 443\n" +
		"pass out quick proto udp to any port { 67 68 546 547 }\n" +
		"pass out quick to 169.254.0.0/16\n" +
		"pass out quick to fe80::/10\n" +
		"pass out quick to { 10.0.0.0/8 172.16.0.0/12 192.168.0.0/16 }\n"
	require.Equal(t, want, client.RenderPFRuleset("utun3", nodeAddrs, rpcAndWSAddrs, true))
}

func TestRenderPFRuleset_NoInterface(t *testing.T) {
	t.Parallel()

	rules := client.RenderPFRuleset("", []netip.Addr{netip.MustParseAddr("203.0.113.1")}, nil, false)
	require.Contains(t, rules, "block drop out all\n")
	require.NotContains(t, rules, "pass out quick on  all")
	require.NotContains(t, rules, "pass out quick on utun")
	require.NotContains(t, rules, "10.0.0.0/8")
}

func TestRunPFCommand_SeparatesDiagnostics(t *testing.T) {
	binary, err := os.Executable()
	require.NoError(t, err)
	dir := t.TempDir()
	reload := filepath.Join(dir, "reload")

	t.Setenv("SENTINEL_PF_COMMAND", "1")
	t.Setenv("SENTINEL_PF_RELOAD", reload)
	t.Setenv("SENTINEL_PF_ROOT", "pass out all\n")

	run := func(ctx context.Context, input, _ string, args ...string) ([]byte, error) {
		return client.RunPFCommand(ctx, input, binary, args...)
	}
	token := filepath.Join(dir, "pf.token")
	fw := client.NewPFFirewall(pfTestAnchor, token, run)
	require.NoError(t, fw.Apply(t.Context(), "utun3", nil, nil, false))

	rules, err := os.ReadFile(reload) //#nosec G304 -- Test-owned temporary file.
	require.NoError(t, err)
	require.Equal(t, pfRootAnchor+"pass out all\n", string(rules))

	saved, err := os.ReadFile(token) //#nosec G304 -- Test-owned temporary file.
	require.NoError(t, err)
	require.Equal(t, "42", string(saved))

	t.Setenv("SENTINEL_PF_ROOT", string(rules))
	loaded, err := client.PFLoaded(t.Context(), run, pfTestAnchor)
	require.NoError(t, err)
	require.True(t, loaded)

	_, err = run(t.Context(), "", "", "fail")
	require.ErrorContains(t, err, "test pf diagnostic")
}
