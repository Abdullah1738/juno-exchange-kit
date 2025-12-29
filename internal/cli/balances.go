package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"math"
	"strings"
	"time"

	"github.com/Abdullah1738/juno-sdk-go/junocashd"
	"github.com/Abdullah1738/juno-sdk-go/junoscan"
)

func runBalances(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("balances", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	var common commonFlags
	common.bind(fs)
	var rpc rpcFlags
	rpc.bind(fs)
	var services servicesFlags
	services.bind(fs)

	var minconf int64
	var doSync bool
	fs.Int64Var(&minconf, "minconf", 1, "minimum confirmations for spendable (0 = include unconfirmed)")
	fs.BoolVar(&doSync, "sync", false, "sync from juno-scan before reporting")

	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		return writeErr(stdout, stderr, common.jsonOut, "invalid_request", "balances takes no positional args")
	}
	if minconf < 0 {
		return writeErr(stdout, stderr, common.jsonOut, "invalid_request", "minconf must be >= 0")
	}

	dataDir, err := common.resolvedDataDir()
	if err != nil {
		return writeErr(stdout, stderr, common.jsonOut, "invalid_request", err.Error())
	}
	st, cleanup, err := openStore(dataDir)
	if err != nil {
		return writeErr(stdout, stderr, common.jsonOut, "io_error", err.Error())
	}
	defer cleanup()

	scanURL := services.resolvedScanURL()
	if scanURL == "" {
		return writeErr(stdout, stderr, common.jsonOut, "invalid_request", "scan url required (set --scan-url or JUNO_SCAN_URL)")
	}
	sc, err := junoscan.New(scanURL)
	if err != nil {
		return writeErr(stdout, stderr, common.jsonOut, "invalid_request", err.Error())
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	if doSync {
		_, _ = syncWallet(ctx, st, sc, "hot", io.Discard, true)
		_, _ = syncWallet(ctx, st, sc, "cold", io.Discard, true)
	}

	liabilities, err := st.SumAccountBalances(ctx)
	if err != nil {
		return writeErr(stdout, stderr, common.jsonOut, "db_error", err.Error())
	}

	var tipHeight int64
	if minconf > 0 {
		rpcURL, rpcUser, rpcPass, err := rpc.resolved()
		if err != nil {
			return writeErr(stdout, stderr, common.jsonOut, "invalid_request", err.Error())
		}
		info, err := junocashd.New(rpcURL, rpcUser, rpcPass).GetBlockchainInfo(ctx)
		if err != nil {
			return writeErr(stdout, stderr, common.jsonOut, "node_rpc_error", err.Error())
		}
		tipHeight = info.Blocks
	}

	hot, err := walletBalanceSummary(ctx, sc, "hot", tipHeight, minconf)
	if err != nil {
		return writeErr(stdout, stderr, common.jsonOut, "scan_error", err.Error())
	}
	cold, err := walletBalanceSummary(ctx, sc, "cold", tipHeight, minconf)
	if err != nil {
		return writeErr(stdout, stderr, common.jsonOut, "scan_error", err.Error())
	}

	assets := hot.TotalUnspentZat + cold.TotalUnspentZat
	equity := assets - liabilities

	hotDepositCount, err := st.CountDepositAddresses(ctx, "hot", nil)
	if err != nil {
		return writeErr(stdout, stderr, common.jsonOut, "db_error", err.Error())
	}

	if common.jsonOut {
		return writeOK(stdout, true, map[string]any{
			"minconf":        minconf,
			"tip_height":     tipHeight,
			"assets_zat":     assets,
			"liabilities_zat": liabilities,
			"equity_zat":     equity,
			"wallets": map[string]any{
				"hot": map[string]any{
					"total_unspent_zat": hot.TotalUnspentZat,
					"spendable_zat":     hot.SpendableZat,
					"unspent_notes":     hot.UnspentNotes,
					"spendable_notes":   hot.SpendableNotes,
					"deposit_addresses": hotDepositCount,
				},
				"cold": map[string]any{
					"total_unspent_zat": cold.TotalUnspentZat,
					"spendable_zat":     cold.SpendableZat,
					"unspent_notes":     cold.UnspentNotes,
					"spendable_notes":   cold.SpendableNotes,
				},
			},
			"synced":         doSync,
			"computed_at_utc": time.Now().UTC().Format(time.RFC3339),
		})
	}

	fmt.Fprintf(stdout, "assets_zat=%d liabilities_zat=%d equity_zat=%d\n", assets, liabilities, equity)
	fmt.Fprintf(stdout, "hot total_unspent_zat=%d spendable_zat=%d unspent_notes=%d spendable_notes=%d deposit_addresses=%d\n", hot.TotalUnspentZat, hot.SpendableZat, hot.UnspentNotes, hot.SpendableNotes, hotDepositCount)
	fmt.Fprintf(stdout, "cold total_unspent_zat=%d spendable_zat=%d unspent_notes=%d spendable_notes=%d\n", cold.TotalUnspentZat, cold.SpendableZat, cold.UnspentNotes, cold.SpendableNotes)
	return 0
}

type walletBalance struct {
	TotalUnspentZat int64
	SpendableZat    int64
	UnspentNotes    int
	SpendableNotes  int
}

func walletBalanceSummary(ctx context.Context, sc *junoscan.Client, walletID string, tipHeight int64, minconf int64) (walletBalance, error) {
	raw, err := sc.ListWalletNotes(ctx, strings.TrimSpace(walletID), true)
	if err != nil {
		return walletBalance{}, err
	}

	var total int64
	var spendable int64
	unspentNotes := 0
	spendableNotes := 0
	for _, n := range raw {
		if n.ValueZat <= 0 {
			continue
		}
		if n.ValueZat > int64(math.MaxInt64-total) {
			return walletBalance{}, fmt.Errorf("wallet %s: balance overflow", walletID)
		}
		total += n.ValueZat
		unspentNotes++

		if minconf == 0 {
			if n.ValueZat > int64(math.MaxInt64-spendable) {
				return walletBalance{}, fmt.Errorf("wallet %s: balance overflow", walletID)
			}
			spendable += n.ValueZat
			spendableNotes++
			continue
		}
		if n.Height <= 0 || tipHeight <= 0 || tipHeight < n.Height {
			continue
		}
		conf := tipHeight - n.Height + 1
		if conf >= minconf {
			if n.ValueZat > int64(math.MaxInt64-spendable) {
				return walletBalance{}, fmt.Errorf("wallet %s: balance overflow", walletID)
			}
			spendable += n.ValueZat
			spendableNotes++
		}
	}

	return walletBalance{
		TotalUnspentZat: total,
		SpendableZat:    spendable,
		UnspentNotes:    unspentNotes,
		SpendableNotes:  spendableNotes,
	}, nil
}
