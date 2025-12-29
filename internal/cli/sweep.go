package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/Abdullah1738/juno-exchange-kit/internal/keys"
	keysffi "github.com/Abdullah1738/juno-exchange-kit/internal/keys/ffi"
	"github.com/Abdullah1738/juno-exchange-kit/internal/store"
	"github.com/Abdullah1738/juno-exchange-kit/internal/txplan"
	"github.com/Abdullah1738/juno-sdk-go/junobroadcast"
	"github.com/Abdullah1738/juno-sdk-go/junocashd"
	"github.com/Abdullah1738/juno-sdk-go/junoscan"
	"github.com/Abdullah1738/juno-sdk-go/types"
)

func runSweep(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "sweep: missing subcommand")
		return 2
	}
	switch args[0] {
	case "consolidate":
		return runSweepConsolidate(args[1:], stdout, stderr)
	case "to-cold":
		return runSweepToCold(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "sweep: unknown subcommand: %s\n", args[0])
		return 2
	}
}

type sweepDeps struct {
	st *store.Store

	uaHRP    string
	coinType uint32

	hotWalletUFVK  string
	hotSeedPath    string
	coldWalletUFVK string

	rpc       *junocashd.Client
	scan      *junoscan.Client
	bcast     *junobroadcast.Client
	deriver   keys.Deriver
	txsignBin string
}

func loadSweepDeps(ctx context.Context, dataDir string, rpc rpcFlags, services servicesFlags, txsignBin string) (*sweepDeps, func(), int) {
	st, cleanup, err := openStore(dataDir)
	if err != nil {
		return nil, nil, 1
	}

	uaHRP, ok, err := st.Meta(ctx, "ua_hrp")
	if err != nil || !ok || strings.TrimSpace(uaHRP) == "" {
		cleanup()
		return nil, nil, 1
	}
	coinTypeStr, ok, err := st.Meta(ctx, "coin_type")
	if err != nil || !ok {
		cleanup()
		return nil, nil, 1
	}
	coinTypeU64, err := strconv.ParseUint(strings.TrimSpace(coinTypeStr), 10, 32)
	if err != nil || coinTypeU64 == 0 {
		cleanup()
		return nil, nil, 1
	}

	hot, ok, err := st.Wallet(ctx, "hot")
	if err != nil || !ok {
		cleanup()
		return nil, nil, 1
	}
	cold, ok, err := st.Wallet(ctx, "cold")
	if err != nil || !ok {
		cleanup()
		return nil, nil, 1
	}

	rpcURL, rpcUser, rpcPass, err := rpc.resolved()
	if err != nil {
		cleanup()
		return nil, nil, 1
	}
	scanURL := services.resolvedScanURL()
	broadcastURL := services.resolvedBroadcastURL()
	if scanURL == "" || broadcastURL == "" {
		cleanup()
		return nil, nil, 1
	}
	sc, err := junoscan.New(scanURL)
	if err != nil {
		cleanup()
		return nil, nil, 1
	}
	bc, err := junobroadcast.New(broadcastURL)
	if err != nil {
		cleanup()
		return nil, nil, 1
	}

	if strings.TrimSpace(txsignBin) == "" {
		txsignBin = txsignBinFromEnv()
	}

	deps := &sweepDeps{
		st:             st,
		uaHRP:          uaHRP,
		coinType:       uint32(coinTypeU64),
		hotWalletUFVK:  strings.TrimSpace(hot.UFVK),
		hotSeedPath:    strings.TrimSpace(hot.SeedPath),
		coldWalletUFVK: strings.TrimSpace(cold.UFVK),
		rpc:            junocashd.New(rpcURL, rpcUser, rpcPass),
		scan:           sc,
		bcast:          bc,
		deriver:        keysffi.New(),
		txsignBin:      txsignBin,
	}
	return deps, cleanup, 0
}

func runSweepConsolidate(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("sweep consolidate", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	var common commonFlags
	common.bind(fs)
	var rpc rpcFlags
	rpc.bind(fs)
	var services servicesFlags
	services.bind(fs)

	var minconf int64
	var expiryOffset uint
	var waitConf int64
	var txsignBin string

	fs.Int64Var(&minconf, "minconf", 1, "minimum confirmations")
	fs.UintVar(&expiryOffset, "expiry-offset", 40, "expiry height offset from chain tip")
	fs.Int64Var(&waitConf, "wait-confirmations", 1, "wait for N confirmations (0 = don't wait)")
	fs.StringVar(&txsignBin, "txsign-bin", "", "path to juno-txsign binary")

	if err := fs.Parse(args); err != nil {
		return 2
	}

	dataDir, err := common.resolvedDataDir()
	if err != nil {
		return writeErr(stdout, stderr, common.jsonOut, string(types.ErrCodeInvalidRequest), err.Error())
	}

	ctx, cancel := context.WithTimeout(context.Background(), 7*time.Minute)
	defer cancel()

	deps, cleanup, code := loadSweepDeps(ctx, dataDir, rpc, services, txsignBin)
	if code != 0 {
		return writeErr(stdout, stderr, common.jsonOut, string(types.ErrCodeInvalidRequest), "missing config (run `init`) or missing env urls")
	}
	defer cleanup()

	_ = deps.scan.UpsertWallet(ctx, "hot", deps.hotWalletUFVK)

	toAddr, err := deps.st.NextInternalAddress(ctx, "hot", func(index uint32) (string, error) {
		return deps.deriver.AddressFromUFVK(deps.hotWalletUFVK, deps.uaHRP, keys.ScopeInternal, index)
	})
	if err != nil {
		return writeErr(stdout, stderr, common.jsonOut, "keys_error", err.Error())
	}

	plans, _, err := txplan.PlanSweepMany(ctx, txplan.SendConfig{
		RPC:              deps.rpc,
		Scan:             deps.scan,
		Wallet:           "hot",
		CoinType:         deps.coinType,
		Account:          0,
		MinConfirmations: minconf,
		ExpiryOffset:     uint32(expiryOffset),
	}, toAddr, "", toAddr, 0)
	if err != nil {
		return writeErr(stdout, stderr, common.jsonOut, string(types.ErrCodeNoLiquidityInHot), err.Error())
	}

	type txOut struct {
		TxID   string `json:"txid"`
		FeeZat int64  `json:"fee_zat"`
	}
	results := make([]txOut, 0, len(plans))

	signedTxs := make([]string, 0, len(plans))
	fees := make([]int64, 0, len(plans))
	for _, p := range plans {
		signed, err := signTxPlan(ctx, deps.txsignBin, deps.hotSeedPath, p)
		if err != nil {
			return writeErr(stdout, stderr, common.jsonOut, "sign_failed", err.Error())
		}
		feeI64, err := strconv.ParseInt(strings.TrimSpace(signed.FeeZat), 10, 64)
		if err != nil || feeI64 < 0 {
			return writeErr(stdout, stderr, common.jsonOut, "sign_failed", "invalid fee returned by signer")
		}
		signedTxs = append(signedTxs, signed.RawTxHex)
		fees = append(fees, feeI64)
	}

	// Submit all txs first, then optionally wait. This avoids serial confirmation waits
	// when multiple txs land in the same block.
	txids := make([]string, 0, len(signedTxs))
	for i, raw := range signedTxs {
		sub, err := deps.bcast.Submit(ctx, raw, nil)
		if err != nil {
			return writeErr(stdout, stderr, common.jsonOut, "broadcast_failed", err.Error())
		}
		txids = append(txids, sub.TxID)
		results = append(results, txOut{TxID: sub.TxID, FeeZat: fees[i]})
	}

	if waitConf > 0 {
		for _, txid := range txids {
			if _, err := deps.bcast.WaitForConfirmations(ctx, txid, waitConf); err != nil {
				return writeErr(stdout, stderr, common.jsonOut, "broadcast_failed", err.Error())
			}
		}
	}

	if common.jsonOut {
		return writeOK(stdout, true, map[string]any{"txs": results, "kind": "consolidate"})
	}
	for _, r := range results {
		fmt.Fprintln(stdout, r.TxID)
	}
	return 0
}

func runSweepToCold(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("sweep to-cold", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	var common commonFlags
	common.bind(fs)
	var rpc rpcFlags
	rpc.bind(fs)
	var services servicesFlags
	services.bind(fs)

	var minconf int64
	var expiryOffset uint
	var waitConf int64
	var txsignBin string

	fs.Int64Var(&minconf, "minconf", 1, "minimum confirmations")
	fs.UintVar(&expiryOffset, "expiry-offset", 40, "expiry height offset from chain tip")
	fs.Int64Var(&waitConf, "wait-confirmations", 1, "wait for N confirmations (0 = don't wait)")
	fs.StringVar(&txsignBin, "txsign-bin", "", "path to juno-txsign binary")

	if err := fs.Parse(args); err != nil {
		return 2
	}

	dataDir, err := common.resolvedDataDir()
	if err != nil {
		return writeErr(stdout, stderr, common.jsonOut, string(types.ErrCodeInvalidRequest), err.Error())
	}

	ctx, cancel := context.WithTimeout(context.Background(), 7*time.Minute)
	defer cancel()

	deps, cleanup, code := loadSweepDeps(ctx, dataDir, rpc, services, txsignBin)
	if code != 0 {
		return writeErr(stdout, stderr, common.jsonOut, string(types.ErrCodeInvalidRequest), "missing config (run `init`) or missing env urls")
	}
	defer cleanup()

	_ = deps.scan.UpsertWallet(ctx, "hot", deps.hotWalletUFVK)
	_ = deps.scan.UpsertWallet(ctx, "cold", deps.coldWalletUFVK)

	coldAddr, err := deps.deriver.AddressFromUFVK(deps.coldWalletUFVK, deps.uaHRP, keys.ScopeExternal, 0)
	if err != nil {
		if errors.Is(err, keys.ErrUnavailable) {
			return writeErr(stdout, stderr, common.jsonOut, "keys_unavailable", "key derivation unavailable (build with CGO and run `make rust-build`)")
		}
		return writeErr(stdout, stderr, common.jsonOut, "keys_error", err.Error())
	}

	plans, _, err := txplan.PlanSweepMany(ctx, txplan.SendConfig{
		RPC:              deps.rpc,
		Scan:             deps.scan,
		Wallet:           "hot",
		CoinType:         deps.coinType,
		Account:          0,
		MinConfirmations: minconf,
		ExpiryOffset:     uint32(expiryOffset),
	}, coldAddr, "", coldAddr, 0)
	if err != nil {
		return writeErr(stdout, stderr, common.jsonOut, string(types.ErrCodeNoLiquidityInHot), err.Error())
	}

	type txOut struct {
		TxID   string `json:"txid"`
		FeeZat int64  `json:"fee_zat"`
	}
	results := make([]txOut, 0, len(plans))

	signedTxs := make([]string, 0, len(plans))
	fees := make([]int64, 0, len(plans))
	for _, p := range plans {
		signed, err := signTxPlan(ctx, deps.txsignBin, deps.hotSeedPath, p)
		if err != nil {
			return writeErr(stdout, stderr, common.jsonOut, "sign_failed", err.Error())
		}
		feeI64, err := strconv.ParseInt(strings.TrimSpace(signed.FeeZat), 10, 64)
		if err != nil || feeI64 < 0 {
			return writeErr(stdout, stderr, common.jsonOut, "sign_failed", "invalid fee returned by signer")
		}
		signedTxs = append(signedTxs, signed.RawTxHex)
		fees = append(fees, feeI64)
	}

	txids := make([]string, 0, len(signedTxs))
	for i, raw := range signedTxs {
		sub, err := deps.bcast.Submit(ctx, raw, nil)
		if err != nil {
			return writeErr(stdout, stderr, common.jsonOut, "broadcast_failed", err.Error())
		}
		txids = append(txids, sub.TxID)
		results = append(results, txOut{TxID: sub.TxID, FeeZat: fees[i]})
	}

	if waitConf > 0 {
		for _, txid := range txids {
			if _, err := deps.bcast.WaitForConfirmations(ctx, txid, waitConf); err != nil {
				return writeErr(stdout, stderr, common.jsonOut, "broadcast_failed", err.Error())
			}
		}
	}

	if common.jsonOut {
		return writeOK(stdout, true, map[string]any{"txs": results, "kind": "to_cold"})
	}
	for _, r := range results {
		fmt.Fprintln(stdout, r.TxID)
	}
	return 0
}
