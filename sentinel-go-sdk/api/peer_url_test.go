package api_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sentinel-official/sentinel-go-sdk/v3/api"
)

func TestParsePeerURL(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		raw  string
		want api.PeerURL
		ok   bool
	}{
		{
			name: "default port",
			raw:  "https://node.example",
			want: api.PeerURL{Host: "node.example", Port: 443},
			ok:   true,
		},
		{
			name: "empty",
			raw:  "",
		},
		{
			name: "empty port",
			raw:  "https://node.example:",
		},
		{
			name: "fragment",
			raw:  "https://node.example#f",
		},
		{
			name: "host label",
			raw:  "https://-bad.example",
		},
		{
			name: "ipv4",
			raw:  "https://203.0.113.5:8443",
			want: api.PeerURL{Host: "203.0.113.5", Port: 8443},
			ok:   true,
		},
		{
			name: "ipv6",
			raw:  "https://[2001:db8::1]:8443",
			want: api.PeerURL{Host: "2001:db8::1", Port: 8443},
			ok:   true,
		},
		{
			name: "ipv6 zone",
			raw:  "https://[fe80::1%25eth0]:8443",
		},
		{
			name: "lowercased with port",
			raw:  "https://Node.Example:8443",
			want: api.PeerURL{Host: "node.example", Port: 8443},
			ok:   true,
		},
		{
			name: "no host",
			raw:  "https://",
		},
		{
			name: "path",
			raw:  "https://node.example/v1",
		},
		{
			name: "port too large",
			raw:  "https://node.example:65536",
		},
		{
			name: "port zero",
			raw:  "https://node.example:0",
		},
		{
			name: "query",
			raw:  "https://node.example/?x=1",
		},
		{
			name: "raw too long",
			raw:  "https://" + strings.Repeat("a", 60) + "." + strings.Repeat("b", 60) + ".example",
		},
		{
			name: "missing scheme",
			raw:  "node.example:443",
		},
		{
			name: "scheme",
			raw:  "http://node.example",
		},
		{
			name: "trailing slash",
			raw:  "https://node.example/",
			want: api.PeerURL{Host: "node.example", Port: 443},
			ok:   true,
		},
		{
			name: "user info",
			raw:  "https://user@node.example",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := api.ParsePeerURL(tc.raw)
			if !tc.ok {
				require.ErrorIs(t, err, api.ErrInvalidPeerURL)

				return
			}

			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestParsePeerURL_QuotesInput(t *testing.T) {
	t.Parallel()

	_, err := api.ParsePeerURL("http://a.example/\x1b[31m\n")

	require.ErrorIs(t, err, api.ErrInvalidPeerURL)
	require.NotContains(t, err.Error(), "\x1b[31m")
	require.Contains(t, err.Error(), `"http://a.example/\x1b[31m\n"`)
}

func TestPeerURL_Addr(t *testing.T) {
	t.Parallel()

	require.Equal(t, "[2001:db8::1]:8443", api.PeerURL{Host: "2001:db8::1", Port: 8443}.Addr())
	require.Equal(t, "node.example:443", api.PeerURL{Host: "node.example", Port: 443}.Addr())
}

func TestPeerURL_String(t *testing.T) {
	t.Parallel()

	require.Equal(t, "https://[2001:db8::1]:8443", api.PeerURL{Host: "2001:db8::1", Port: 8443}.String())
	require.Equal(t, "https://node.example:443", api.PeerURL{Host: "node.example", Port: 443}.String())
}
