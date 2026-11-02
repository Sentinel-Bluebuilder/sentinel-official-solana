package openvpn_test

import (
	"bufio"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sentinel-official/sentinel-go-sdk/v3/openvpn"
)

func TestManagement_AuthAndStatus(t *testing.T) {
	t.Parallel()

	server, clientConn := net.Pipe()
	credentials := map[string]string{"user-1": "secret"}

	m := openvpn.NewManagement(credentials)
	done := make(chan error, 1)
	serve := func() {
		done <- openvpn.ManagementServe(m, bufio.NewReader(clientConn), clientConn)
	}

	go serve()

	w := bufio.NewWriter(server)
	r := bufio.NewReader(server)

	_, _ = w.WriteString(
		">CLIENT:CONNECT,7,2\n>CLIENT:ENV,username=user-1\n>CLIENT:ENV,password=secret\n>CLIENT:ENV,END\n",
	)
	require.NoError(t, w.Flush())

	line, err := r.ReadString('\n')
	require.NoError(t, err)
	require.Equal(t, "client-auth-nt 7 2\n", line)

	_, _ = w.WriteString("SUCCESS: client-auth command succeeded\n>CLIENT:ESTABLISHED,7\n")
	require.NoError(t, w.Flush())

	established := func() bool { return openvpn.ManagementHasClient(m, "user-1", "7") }
	require.Eventually(t, established, time.Second, 10*time.Millisecond)

	_, _ = w.WriteString(">CLIENT:CONNECT,8,1\n>CLIENT:ENV,username=user-1\n>CLIENT:ENV,password=wrong\n>CLIENT:ENV,END\n")
	require.NoError(t, w.Flush())

	line, err = r.ReadString('\n')
	require.NoError(t, err)
	require.Equal(t, "client-deny 8 1 \"bad credentials\"\n", line)

	_, _ = w.WriteString("SUCCESS: client-deny command succeeded\n")
	require.NoError(t, w.Flush())

	stats := openvpn.ParseStatus(statusV3Fixture)
	require.EqualValues(t, 1500, stats["user-1"].RxBytes)
	require.EqualValues(t, 2500, stats["user-1"].TxBytes)

	type result struct {
		err   error
		reply string
	}

	results := make(chan result, 1)
	query := func() {
		reply, err := openvpn.ManagementExec(m, "status 3")
		results <- result{
			err:   err,
			reply: reply,
		}
	}

	go query()

	line, err = r.ReadString('\n')
	require.NoError(t, err)
	require.Equal(t, "status 3\n", line)

	_, _ = w.WriteString(">CLIENT:ESTABLISHED,9\n" + statusV3Fixture)
	require.NoError(t, w.Flush())

	outcome := <-results
	require.NoError(t, outcome.err)
	require.Equal(t, openvpn.ParseStatus(statusV3Fixture), openvpn.ParseStatus(outcome.reply))

	_, _ = w.WriteString(">CLIENT:DISCONNECT,7\n")
	require.NoError(t, w.Flush())

	disconnected := func() bool { return !openvpn.ManagementHasClient(m, "user-1", "7") }
	require.Eventually(t, disconnected, time.Second, 10*time.Millisecond)

	_ = clientConn.Close()

	require.NoError(t, <-done)
}

func TestManagement_ServeAuthWriteFailure(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		command  string
		name     string
		password string
	}{
		{command: "client-auth-nt 7 2", name: "allow", password: "secret"},
		{command: "client-deny 7 2", name: "deny", password: "wrong"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			writeErr := errors.New("auth write failed")
			m := openvpn.NewManagement(map[string]string{"user-1": "secret"})
			input := ">CLIENT:CONNECT,7,2\n>CLIENT:ENV,username=user-1\n" +
				">CLIENT:ENV,password=" + tc.password + "\n>CLIENT:ENV,END\n"

			err := openvpn.ManagementServe(m, bufio.NewReader(strings.NewReader(input)), failingWriter{err: writeErr})
			require.ErrorIs(t, err, writeErr)
			require.ErrorContains(t, err, tc.command)
			require.False(t, openvpn.ManagementHasClient(m, "user-1", "7"))
		})
	}
}
