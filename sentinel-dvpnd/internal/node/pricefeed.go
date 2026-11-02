//go:build linux

package node

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"time"

	"github.com/solana-foundation/solana-go/v2"

	"github.com/sentinel-official/sentinel-go-sdk/v3/tx"
	"github.com/sentinel-official/sentinel-programs/clients/go/dvpn"
)

const (
	baseUnitsPerToken = 1_000_000
	basisPoints       = 10_000
	coinGeckoURL      = "https://api.coingecko.com/api/v3/simple/price"
	priceDriftBps     = 500
	priceFeedCoinID   = "sentinel"
	priceFeedCurrency = "usd"
	priceFeedTimeout  = 10 * time.Second
)

func (r *Runtime) updatePrices(ctx context.Context) error {
	quote, err := fetchQuote(ctx, r.cfg.PriceFeed.APIKey)
	if err != nil {
		return err
	}

	_, node := r.chainState()

	want := dvpn.NodePrices{
		ChannelPricePerGB:   toBaseUnits(r.cfg.PriceFeed.ChannelPerGBUSD, quote),
		ChannelPricePerHour: toBaseUnits(r.cfg.PriceFeed.ChannelPerHourUSD, quote),
		LeasePricePerHour:   toBaseUnits(r.cfg.PriceFeed.LeasePerHourUSD, quote),
	}
	if withinDrift(node.ChannelPricePerGB, want.ChannelPricePerGB) &&
		withinDrift(node.ChannelPricePerHour, want.ChannelPricePerHour) &&
		withinDrift(node.LeasePricePerHour, want.LeasePricePerHour) {
		return nil
	}

	grant, err := r.programClient.GrantAddress(ctx, r.owner, r.signer, dvpn.PermUpdateNode, r.address)
	if err != nil {
		return err
	}

	instruction, err := dvpn.BuildUpdateNodeInstruction(r.programClient.ProgramID, dvpn.UpdateNodeParams{
		Grant:  grant,
		Node:   r.address,
		Prices: &want,
		Signer: r.signer,
	})
	if err != nil {
		return err
	}

	_, err = r.sender.Send(ctx, r.privateKey, []solana.Instruction{instruction}, tx.WithLabel("update_node"))

	return err
}

func fetchQuote(ctx context.Context, apiKey string) (float64, error) {
	query := url.Values{"ids": {priceFeedCoinID}, "vs_currencies": {priceFeedCurrency}}
	target := coinGeckoURL + "?" + query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return 0, fmt.Errorf("building coingecko request: %w", err)
	}

	if apiKey != "" {
		req.Header.Set("X-Cg-Demo-Api-Key", apiKey)
	}

	client := &http.Client{Timeout: priceFeedTimeout}

	response, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("fetching coingecko price: %w", err)
	}

	defer func() { _ = response.Body.Close() }()

	var body map[string]map[string]float64
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		return 0, fmt.Errorf("decoding coingecko response: %w", err)
	}

	price, ok := body[priceFeedCoinID][priceFeedCurrency]
	if !ok || price <= 0 {
		return 0, fmt.Errorf("no %s quote for %s", priceFeedCurrency, priceFeedCoinID)
	}

	return price, nil
}

func toBaseUnits(usd, quote float64) uint64 {
	if usd <= 0 || quote <= 0 {
		return 0
	}

	return uint64(math.Round(usd / quote * baseUnitsPerToken))
}

func withinDrift(current, want uint64) bool {
	if current == want {
		return true
	}

	diff := max(current, want) - min(current, want)

	return diff*basisPoints <= max(current, want)*priceDriftBps
}
