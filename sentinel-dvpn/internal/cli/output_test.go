package cli_test

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/descriptorpb"

	"github.com/sentinel-official/sentinel-dvpn/internal/cli"
	daemonv1 "github.com/sentinel-official/sentinel-go-sdk/v3/daemon/v1"
	"github.com/sentinel-official/sentinel-programs/clients/go/dvpn"
)

func TestPrinter_Print_ProtoEmitsZeroValues(t *testing.T) {
	t.Parallel()

	check := func(value any, want string) {
		t.Helper()

		var buf bytes.Buffer
		require.NoError(t, cli.NewPrinter(&buf, cli.FormatJSON).Print(value))

		var compact bytes.Buffer
		require.NoError(t, json.Compact(&compact, buf.Bytes()))
		require.Equal(t, want, compact.String())
	}

	check(&daemonv1.ListSessionsResponse{}, `{"sessions":[]}`)
	check(&daemonv1.ListRosterNodesResponse{Nodes: []*daemonv1.RosterNode{{}}},
		`{"nodes":[{"node":"","plan":"","live":false,"probed_at":0,"lease":""}]}`)
	check(&daemonv1.Usage{RxBytes: 1 << 40}, `{"rx_bytes":1099511627776,"tx_bytes":0,"seconds":0}`)

	var enumOutput bytes.Buffer
	require.NoError(t, cli.NewPrinter(&enumOutput, cli.FormatJSON).Print(
		&descriptorpb.FieldDescriptorProto{Type: descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum()}))
	require.Contains(t, enumOutput.String(), `"type": "TYPE_STRING"`)
}

func TestPrinter_Print_ProtoTableIncludesZeroValues(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	require.NoError(t, cli.NewPrinter(&buf, cli.FormatTable).Print(&daemonv1.RosterNode{Node: "node-addr"}))
	require.Regexp(t, `(?m)^\|\s*live\s*\|\s*false\s*\|$`, buf.String())
	require.Regexp(t, `(?m)^\|\s*probed_at\s*\|\s*0\s*\|$`, buf.String())
}

func TestPrinter_Print_TableAlignsOptionalColumns(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	views := []struct {
		First string `json:"first,omitempty"`
		Later string `json:"later,omitempty"`
	}{
		{First: "one"},
		{Later: "two"},
	}

	require.NoError(t, cli.NewPrinter(&buf, cli.FormatTable).Print(views))
	require.Regexp(t, `(?m)^\|\s*FIRST\s*\|\s*LATER\s*\|$`, buf.String())
	require.Regexp(t, `(?m)^\|\s*one\s*\|\s*\|$`, buf.String())
	require.Regexp(t, `(?m)^\|\s*\|\s*two\s*\|$`, buf.String())
}

func TestPrinter_Print_TableEscapesControlCharacters(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		text string
		want string
	}{
		{
			name: "ansi escape and newline",
			text: "a\x1b[31mb\n",
			want: `a\x1b[31mb\n`,
		},
		{
			name: "c1 control",
			text: "\u009b31m",
			want: `\u009b31m`,
		},
		{
			name: "invalid utf8",
			text: "a\xffb",
			want: `a\xffb`,
		},
		{
			name: "bidi override",
			text: "a\u202eb",
			want: `a\u202eb`,
		},
		{
			name: "printable non-ascii unchanged",
			text: "caf\u00e9 \"q\"",
			want: "caf\u00e9 \"q\"",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer

			value := struct {
				Name string `json:"name"`
			}{Name: tc.text}
			require.NoError(t, cli.NewPrinter(&buf, cli.FormatTable).Print(value))
			require.Contains(t, buf.String(), tc.want)
			require.NotContains(t, buf.String(), "\x1b")
			require.NotContains(t, buf.String(), "\u009b")
			require.NotContains(t, buf.String(), "\u202e")
		})
	}
}

func TestPrinter_Print_TableEscapesFieldNames(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	value := struct {
		Name string `json:"a\x1bb"`
	}{Name: "v"}
	require.NoError(t, cli.NewPrinter(&buf, cli.FormatTable).Print(value))
	require.Contains(t, buf.String(), `a\x1bb`)
	require.NotContains(t, buf.String(), "\x1b")
}

func TestPrinter_Print_TableFlattensNestedSlice(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	view := []struct {
		Value dvpn.Node `json:"value"`
	}{
		{Value: dvpn.Node{Country: [2]uint8{'D', 'E'}}},
	}

	require.NoError(t, cli.NewPrinter(&buf, cli.FormatTable).Print(view))
	require.Contains(t, buf.String(), "VALUE.COUNTRY")
	require.Regexp(t, `DE\s+\|`, buf.String())
}

func TestPrinter_Print_TableFlattensNestedStruct(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	node := dvpn.Node{
		Country: [2]uint8{'D', 'E'},
		URILen:  11,
	}
	copy(node.URI[:], "https://a:1")
	view := struct {
		Value dvpn.Node `json:"value"`
	}{Value: node}

	require.NoError(t, cli.NewPrinter(&buf, cli.FormatTable).Print(view))
	require.Regexp(t, `value.country\s+\|\s+DE`, buf.String())
	require.Regexp(t, `value.uri\s+\|\s+https://a:1`, buf.String())
}

func TestPrinter_Print_TablePrintsScalarSlice(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	require.NoError(t, cli.NewPrinter(&buf, cli.FormatTable).Print([]string{"one", "two"}))
	require.Equal(t, "[one two]\n", buf.String())
}
