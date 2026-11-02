package main_test

import (
	"testing"

	generator "github.com/sentinel-official/sentinel-programs/tools/anchor-go"
)

func TestBatchName(t *testing.T) {
	cases := map[string]string{"Roots": "FetchRootsBatch", "Channel": "FetchChannelBatch"}

	for in, want := range cases {
		if got := generator.BatchName(in); got != want {
			t.Errorf("batchName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestExportedName(t *testing.T) {
	cases := map[string]string{
		"node_id":          "NodeID",
		"uri":              "URI",
		"uri_len":          "URILen",
		"ttl_seconds":      "TTLSeconds",
		"pubkey":           "PublicKey",
		"price_per_gb":     "PricePerGB",
		"fee_bps":          "FeeBps",
		"tls_sha256":       "TLSSHA256",
		"rpc_url":          "RPCURL",
		"json_blob":        "JSONBlob",
		"schedule_kind":    "ScheduleKind",
		"MODE_VOUCHER":     "ModeVoucher",
		"PERM_OPEN_NODE":   "PermOpenNode",
		"NodeId":           "NodeID",
		"NodeID":           "NodeID",
		"CompressedPubkey": "CompressedPublicKey",
	}

	for in, want := range cases {
		if got := generator.ExportedName(in); got != want {
			t.Errorf("exportedName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestUnexportedName(t *testing.T) {
	cases := map[string]string{
		"node_id":    "nodeID",
		"uri":        "uri",
		"shard_id":   "shardID",
		"fee_bps":    "feeBps",
		"type":       "type_",
		"pubkey":     "publicKey",
		"ShardId":    "shardID",
		"rpc_client": "rpcClient",
	}

	for in, want := range cases {
		if got := generator.UnexportedName(in); got != want {
			t.Errorf("unexportedName(%q) = %q, want %q", in, got, want)
		}
	}
}
