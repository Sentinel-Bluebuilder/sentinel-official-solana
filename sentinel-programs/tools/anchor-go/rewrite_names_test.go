package main_test

import (
	"strings"
	"testing"

	generator "github.com/sentinel-official/sentinel-programs/tools/anchor-go"
)

func TestRenameUpstream(t *testing.T) {
	files := map[string][]byte{
		"program-id.go": []byte(`package x

const MODE_VOUCHER = uint8(0x0)

var Account_Channel = [8]byte{1}

type ScheduleKind uint8

const (
	ScheduleKind_Continuous ScheduleKind = iota
)

type Channel struct {
	ChannelId [32]uint8 ` + "`json:\"channelId\"`" + `
	UriLen    uint8     ` + "`json:\"uriLen\"`" + `
	FeeBps    uint16    ` + "`json:\"feeBps\"`" + `
}

func (obj *Channel) Validate() error { return obj.UriLen }

func ParseAccount_Channel(accountData []byte) (*Channel, error) {
	_ = Account_Channel
	return nil, nil
}

// Builds a "open" instruction.
func NewOpenInstruction(
	// Params:
	shardIdParam uint16,

	// Accounts:
	payerAccount solanago.PublicKey,
) error {
	// Serialize ` + "`shardIdParam`" + `:
	return newFieldError("shardIdParam", payerAccount)
}
`),
	}

	if err := generator.RenameUpstream(files); err != nil {
		t.Fatal(err)
	}

	if _, ok := files["program_id.go"]; !ok || len(files) != 1 {
		t.Fatalf("files = %v, want program_id.go", len(files))
	}

	got := string(files["program_id.go"])
	for _, want := range []string{
		"const ModeVoucher = ",
		"ChannelAccountDiscriminator = [8]byte{1}",
		"ScheduleKindContinuous ScheduleKind = iota",
		"ChannelID",
		"URILen",
		"FeeBps",
		`json:"channel_id"`,
		`json:"uri_len"`,
		`json:"fee_bps"`,
		"func (c *Channel) Validate() error { return c.URILen }",
		"func ParseChannelAccount(",
		"func newOpenInstruction(",
		"shardID uint16,",
		"payer solanago.PublicKey,",
		"// Serialize `shardID`:",
		`newFieldError("shardID", payer)`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}

	for _, stale := range []string{"MODE_VOUCHER", "Account_Channel", "_Continuous", "shardIdParam", "payerAccount", "obj"} {
		if strings.Contains(got, stale) {
			t.Errorf("stale %q in:\n%s", stale, got)
		}
	}
}

func TestRenameUpstream_Messages(t *testing.T) {
	files := map[string][]byte{
		"types.go": []byte(`package x

type ScheduleKind uint8

const (
	ScheduleKind_Continuous ScheduleKind = iota
)

func (obj *ScheduleKind) MarshalWithEncoder() error {
	return fmt.Errorf("error while encoding ScheduleKind_Continuous: %w; obj unchanged", errors.New("ScheduleKind_Continuousx"))
}
`),
	}

	if err := generator.RenameUpstream(files); err != nil {
		t.Fatal(err)
	}

	got := string(files["types.go"])
	for _, want := range []string{
		`"error while encoding ScheduleKindContinuous: %w; obj unchanged"`,
		`"ScheduleKind_Continuousx"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}
