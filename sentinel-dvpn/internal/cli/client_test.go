package cli_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sentinel-official/sentinel-dvpn/internal/cli"
	"github.com/sentinel-official/sentinel-programs/clients/go/dvpn"
)

func TestBuildCredential_Variants(t *testing.T) {
	t.Parallel()

	voucher := uint32(dvpn.PositionModeVoucher)
	stream := uint32(dvpn.PositionModeStream)

	cases := []struct {
		channel      string
		mode         *uint32
		name         string
		newChannel   bool
		provider     string
		subscription string
		ticket       bool
		ticketHash   string
		wantErr      error
	}{
		{channel: "chan-1", name: "channel"},
		{mode: &voucher, name: "new channel voucher", newChannel: true},
		{mode: &stream, name: "new channel stream", newChannel: true},
		{name: "new channel without mode", newChannel: true, wantErr: cli.ErrMissingChannelMode},
		{name: "ticket", provider: "prov-1", subscription: "sub-1", ticket: true},
		{name: "ticket missing subscription", provider: "prov-1", ticket: true, wantErr: cli.ErrMissingTicketFields},
		{name: "ticket missing provider", subscription: "sub-1", ticket: true, wantErr: cli.ErrMissingTicketFields},
		{name: "ticket hash", ticketHash: "hash-1"},
		{name: "empty", wantErr: cli.ErrCredentialEmpty},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := cli.BuildCredential(
				tc.channel,
				tc.newChannel,
				tc.mode,
				0,
				tc.ticket,
				tc.provider,
				tc.subscription,
				tc.ticketHash,
			)
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				require.Nil(t, got)

				return
			}

			require.NoError(t, err)
			require.NotNil(t, got)
		})
	}
}

func TestNewClientStartCmd_FlagGroups(t *testing.T) {
	t.Parallel()

	cases := []struct {
		args []string
		name string
		want string
	}{
		{
			args: startArgs(),
			name: "none selected",
			want: "at least one of the flags in the group [channel new-channel ticket ticket-hash] is required",
		},
		{
			args: startArgs("--channel", "chan-1", "--ticket-hash", "hash-1"),
			name: "two selected",
			want: "are set none of the others can be",
		},
		{args: startArgs("--new-channel", "--mode", "bogus"), name: "bad mode", want: "unknown mode"},
		{args: startArgs("--new-channel"), name: "new channel without mode", want: cli.ErrMissingChannelMode.Error()},
		{
			args: startArgs("--mode", "voucher"),
			name: "lone mode allowed",
			want: "at least one of the flags in the group [channel new-channel ticket ticket-hash] is required",
		},
		{args: clientArgs("start", "--channel", "chan-1"), name: "missing node", want: `required flag(s) "node" not set`},
		{args: startArgs("--ticket-hash", ""), name: "empty ticket hash", want: cli.ErrCredentialEmpty.Error()},
		{args: startArgs("--channel", ""), name: "empty channel", want: cli.ErrCredentialEmpty.Error()},
		{args: startArgs("--new-channel=false"), name: "new channel false", want: cli.ErrCredentialEmpty.Error()},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cmd, _ := newRoot(t, "", tc.args...)
			require.ErrorContains(t, cmd.ExecuteContext(t.Context()), tc.want)
		})
	}
}
