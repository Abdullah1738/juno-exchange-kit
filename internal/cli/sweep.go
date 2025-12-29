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

	hotWalletUFVK string
	hotSeedPath   string
	coldWalletUFVK string

	rpc      *junocashd.Client
	scan     *junoscan.Client
	bcast    *junobroadcast.Client
	deriver  keys.Deriver
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
		st:        st,
		uaHRP:     uaHRP,
		coinType:  uint32(coinTypeU64),
		hotWalletUFVK: strings.TrimSpace(hot.UFVK),
		hotSeedPath:   strings.TrimSpace(hot.SeedPath),
		coldWalletUFVK: strings.TrimSpace(cold.UFVK),
		rpc:       junocashd.New(rpcURL, rpcUser, rpcPass),
		scan:      sc,
		bcast:     bc,
		deriver:   keysffi.New(),
		txsignBin: txsignBin,
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

	plan, fee, err := txplan.PlanSweep(ctx, txplan.SendConfig{
		RPC:              deps.rpc,
		Scan:             deps.scan,
		Wallet:           "hot",
		CoinType:         deps.coinType,
		Account:          0,
		MinConfirmations: minconf,
		ExpiryOffset:     uint32(expiryOffset),
	}, toAddr, "", toAddr)
	if err != nil {
		return writeErr(stdout, stderr, common.jsonOut, string(types.ErrCodeNoLiquidityInHot), err.Error())
	}

	signed, err := signTxPlan(ctx, deps.txsignBin, deps.hotSeedPath, plan)
	if err != nil {
		return writeErr(stdout, stderr, common.jsonOut, "sign_failed", err.Error())
	}

	var waitPtr *int64
	if waitConf > 0 {
		waitPtr = &waitConf
	}
	sub, err := deps.bcast.Submit(ctx, signed.RawTxHex, waitPtr)
	if err != nil {
		return writeErr(stdout, stderr, common.jsonOut, "broadcast_failed", err.Error())
	}

	if common.jsonOut {
		return writeOK(stdout, true, map[string]any{"txid": sub.TxID, "fee_zat": fee, "kind": "consolidate"})
	}
	fmt.Fprintln(stdout, sub.TxID)
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

	plan, fee, err := txplan.PlanSweep(ctx, txplan.SendConfig{
		RPC:              deps.rpc,
		Scan:             deps.scan,
		Wallet:           "hot",
		CoinType:         deps.coinType,
		Account:          0,
		MinConfirmations: minconf,
		ExpiryOffset:     uint32(expiryOffset),
	}, coldAddr, "", coldAddr)
	if err != nil {
		return writeErr(stdout, stderr, common.jsonOut, string(types.ErrCodeNoLiquidityInHot), err.Error())
	}

	signed, err := signTxPlan(ctx, deps.txsignBin, deps.hotSeedPath, plan)
	if err != nil {
		return writeErr(stdout, stderr, common.jsonOut, "sign_failed", err.Error())
	}

	var waitPtr *int64
	if waitConf > 0 {
		waitPtr = &waitConf
	}
	sub, err := deps.bcast.Submit(ctx, signed.RawTxHex, waitPtr)
	if err != nil {
		return writeErr(stdout, stderr, common.jsonOut, "broadcast_failed", err.Error())
	}

	if common.jsonOut {
		return writeOK(stdout, true, map[string]any{"txid": sub.TxID, "fee_zat": fee, "kind": "to_cold"})
	}
	fmt.Fprintln(stdout, sub.TxID)
	return 0
}
