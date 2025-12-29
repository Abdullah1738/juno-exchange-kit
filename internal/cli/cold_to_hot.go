package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Abdullah1738/juno-exchange-kit/internal/keys"
	keysffi "github.com/Abdullah1738/juno-exchange-kit/internal/keys/ffi"
	"github.com/Abdullah1738/juno-exchange-kit/internal/txplan"
	"github.com/Abdullah1738/juno-sdk-go/junobroadcast"
	"github.com/Abdullah1738/juno-sdk-go/junocashd"
	"github.com/Abdullah1738/juno-sdk-go/junoscan"
	"github.com/Abdullah1738/juno-sdk-go/types"
)

func runColdToHot(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "cold-to-hot: missing subcommand")
		return 2
	}
	switch args[0] {
	case "plan":
		return runColdToHotPlan(args[1:], stdout, stderr)
	case "sign":
		return runColdToHotSign(args[1:], stdout, stderr)
	case "broadcast":
		return runColdToHotBroadcast(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "cold-to-hot: unknown subcommand: %s\n", args[0])
		return 2
	}
}

func runColdToHotPlan(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("cold-to-hot plan", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	var common commonFlags
	common.bind(fs)

	var rpc rpcFlags
	rpc.bind(fs)

	var services servicesFlags
	services.bind(fs)

	var amountStr string
	var outPath string
	var minconf int64
	var expiryOffset uint

	fs.StringVar(&amountStr, "amount-zat", "", "amount in zatoshis")
	fs.StringVar(&outPath, "out", "", "optional path to write TxPlan JSON")
	fs.Int64Var(&minconf, "minconf", 1, "minimum confirmations")
	fs.UintVar(&expiryOffset, "expiry-offset", 40, "expiry height offset from chain tip")

	if err := fs.Parse(args); err != nil {
		return 2
	}

	amountStr = strings.TrimSpace(amountStr)
	if amountStr == "" {
		return writeErr(stdout, stderr, common.jsonOut, string(types.ErrCodeInvalidRequest), "amount-zat is required")
	}
	amountU64, err := strconv.ParseUint(amountStr, 10, 64)
	if err != nil || amountU64 == 0 {
		return writeErr(stdout, stderr, common.jsonOut, string(types.ErrCodeInvalidRequest), "amount-zat must be a positive integer")
	}

	dataDir, err := common.resolvedDataDir()
	if err != nil {
		return writeErr(stdout, stderr, common.jsonOut, string(types.ErrCodeInvalidRequest), err.Error())
	}

	st, cleanup, err := openStore(dataDir)
	if err != nil {
		return writeErr(stdout, stderr, common.jsonOut, "io_error", err.Error())
	}
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 7*time.Minute)
	defer cancel()

	uaHRP, ok, err := st.Meta(ctx, "ua_hrp")
	if err != nil || !ok || strings.TrimSpace(uaHRP) == "" {
		return writeErr(stdout, stderr, common.jsonOut, string(types.ErrCodeInvalidRequest), "not initialized (run `init`)")
	}
	coinTypeStr, ok, err := st.Meta(ctx, "coin_type")
	if err != nil || !ok {
		return writeErr(stdout, stderr, common.jsonOut, string(types.ErrCodeInvalidRequest), "missing coin_type (run `init`)")
	}
	coinTypeU64, err := strconv.ParseUint(strings.TrimSpace(coinTypeStr), 10, 32)
	if err != nil || coinTypeU64 == 0 {
		return writeErr(stdout, stderr, common.jsonOut, string(types.ErrCodeInvalidRequest), "invalid coin_type")
	}
	coinType := uint32(coinTypeU64)

	hot, ok, err := st.Wallet(ctx, "hot")
	if err != nil || !ok {
		return writeErr(stdout, stderr, common.jsonOut, string(types.ErrCodeInvalidRequest), "hot wallet missing (run `init`)")
	}
	cold, ok, err := st.Wallet(ctx, "cold")
	if err != nil || !ok {
		return writeErr(stdout, stderr, common.jsonOut, string(types.ErrCodeInvalidRequest), "cold wallet missing (run `init`)")
	}

	rpcURL, rpcUser, rpcPass, err := rpc.resolved()
	if err != nil {
		return writeErr(stdout, stderr, common.jsonOut, string(types.ErrCodeInvalidRequest), err.Error())
	}
	scanURL := services.resolvedScanURL()
	if scanURL == "" {
		return writeErr(stdout, stderr, common.jsonOut, string(types.ErrCodeInvalidRequest), "scan url required (set --scan-url or JUNO_SCAN_URL)")
	}
	sc, err := junoscan.New(scanURL)
	if err != nil {
		return writeErr(stdout, stderr, common.jsonOut, string(types.ErrCodeInvalidRequest), err.Error())
	}

	_ = sc.UpsertWallet(ctx, "hot", hot.UFVK)
	_ = sc.UpsertWallet(ctx, "cold", cold.UFVK)

	deriver := keysffi.New()
	hotToAddr, err := st.NextInternalAddress(ctx, "hot", func(index uint32) (string, error) {
		return deriver.AddressFromUFVK(hot.UFVK, uaHRP, keys.ScopeInternal, index)
	})
	if err != nil {
		return writeErr(stdout, stderr, common.jsonOut, "keys_error", err.Error())
	}
	coldChangeAddr, err := st.NextInternalAddress(ctx, "cold", func(index uint32) (string, error) {
		return deriver.AddressFromUFVK(cold.UFVK, uaHRP, keys.ScopeInternal, index)
	})
	if err != nil {
		return writeErr(stdout, stderr, common.jsonOut, "keys_error", err.Error())
	}

	plan, _, err := txplan.PlanRebalance(ctx, txplan.SendConfig{
		RPC:              junocashd.New(rpcURL, rpcUser, rpcPass),
		Scan:             sc,
		Wallet:           "cold",
		CoinType:         coinType,
		Account:          0,
		MinConfirmations: minconf,
		ExpiryOffset:     uint32(expiryOffset),
	}, []txplan.Output{{ToAddress: hotToAddr, AmountZat: amountU64}}, coldChangeAddr)
	if err != nil {
		maxAmountZat, spendableZat, spendableNotes := coldMaxSendable(ctx, junocashd.New(rpcURL, rpcUser, rpcPass), sc, minconf)

		var ce types.CodedError
		if errors.As(err, &ce) && ce.Code == types.ErrCodeNoLiquidityInHot {
			return writeErrWithDetails(stdout, stderr, common.jsonOut, "no_liquidity_in_cold", "NO_LIQUIDITY_IN_COLD", map[string]any{
				"requested_amount_zat": int64(amountU64),
				"max_amount_zat":       maxAmountZat,
				"spendable_zat":        spendableZat,
				"spendable_notes":      spendableNotes,
			})
		}
		return writeErr(stdout, stderr, common.jsonOut, string(types.ErrCodeInvalidRequest), err.Error())
	}

	if outPath != "" {
		b, _ := json.MarshalIndent(plan, "", "  ")
		if err := os.WriteFile(outPath, append(b, '\n'), 0o600); err != nil {
			return writeErr(stdout, stderr, common.jsonOut, "io_error", err.Error())
		}
	}

	if common.jsonOut {
		return writeOK(stdout, true, map[string]any{"txplan": plan})
	}
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(plan)
	return 0
}

func coldMaxSendable(ctx context.Context, rpc *junocashd.Client, sc *junoscan.Client, minconf int64) (maxAmountZat int64, spendableZat int64, spendableNotes int) {
	if rpc == nil || sc == nil || ctx == nil {
		return 0, 0, 0
	}
	if minconf < 0 {
		minconf = 0
	}

	info, err := rpc.GetBlockchainInfo(ctx)
	if err != nil {
		return 0, 0, 0
	}
	tip := info.Blocks
	if tip < 0 {
		return 0, 0, 0
	}

	raw, err := sc.ListWalletNotes(ctx, "cold", true)
	if err != nil {
		return 0, 0, 0
	}

	const maxNotesPerTx = 200
	values := make([]int64, 0, len(raw))
	for _, n := range raw {
		if n.Position == nil || *n.Position < 0 {
			continue
		}
		if n.Height < 0 || tip < n.Height {
			continue
		}
		if minconf > 0 {
			conf := tip - n.Height + 1
			if conf < minconf {
				continue
			}
		}
		if n.ActionIndex < 0 {
			continue
		}
		if n.ValueZat <= 0 {
			continue
		}
		values = append(values, n.ValueZat)
	}

	sort.Slice(values, func(i, j int) bool { return values[i] > values[j] })
	if len(values) > maxNotesPerTx {
		values = values[:maxNotesPerTx]
	}

	var total int64
	for _, v := range values {
		if v > 0 && v > (int64(^uint64(0)>>1)-total) {
			return 0, 0, 0
		}
		total += v
	}
	if total < 0 {
		return 0, 0, 0
	}

	feeU64, err := txplan.RequiredFeeSend(len(values), 1)
	if err != nil || feeU64 > uint64(^uint64(0)>>1) {
		return 0, 0, 0
	}
	fee := int64(feeU64)

	maxAmount := int64(0)
	if total > fee {
		maxAmount = total - fee
	}

	return maxAmount, total, len(values)
}

func runColdToHotSign(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("cold-to-hot sign", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	var common commonFlags
	common.bind(fs)

	var txplanPath string
	var outPath string
	var txsignBin string

	fs.StringVar(&txplanPath, "txplan", "", "path to TxPlan JSON (or - for stdin)")
	fs.StringVar(&outPath, "out", "", "optional path to write raw tx hex")
	fs.StringVar(&txsignBin, "txsign-bin", "", "path to juno-txsign binary")

	if err := fs.Parse(args); err != nil {
		return 2
	}

	txplanPath = strings.TrimSpace(txplanPath)
	if txplanPath == "" {
		return writeErr(stdout, stderr, common.jsonOut, string(types.ErrCodeInvalidRequest), "txplan is required")
	}

	dataDir, err := common.resolvedDataDir()
	if err != nil {
		return writeErr(stdout, stderr, common.jsonOut, string(types.ErrCodeInvalidRequest), err.Error())
	}
	st, cleanup, err := openStore(dataDir)
	if err != nil {
		return writeErr(stdout, stderr, common.jsonOut, "io_error", err.Error())
	}
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 7*time.Minute)
	defer cancel()

	cold, ok, err := st.Wallet(ctx, "cold")
	if err != nil || !ok {
		return writeErr(stdout, stderr, common.jsonOut, string(types.ErrCodeInvalidRequest), "cold wallet missing (run `init`)")
	}

	plan, err := loadTxPlan(txplanPath)
	if err != nil {
		return writeErr(stdout, stderr, common.jsonOut, string(types.ErrCodeInvalidRequest), err.Error())
	}

	if strings.TrimSpace(txsignBin) == "" {
		txsignBin = txsignBinFromEnv()
	}
	signed, err := signTxPlan(ctx, txsignBin, cold.SeedPath, plan)
	if err != nil {
		return writeErr(stdout, stderr, common.jsonOut, "sign_failed", err.Error())
	}

	if outPath != "" {
		if err := os.WriteFile(outPath, []byte(signed.RawTxHex+"\n"), 0o600); err != nil {
			return writeErr(stdout, stderr, common.jsonOut, "io_error", err.Error())
		}
	}

	if common.jsonOut {
		return writeOK(stdout, true, map[string]any{"txid": signed.TxID, "raw_tx_hex": signed.RawTxHex, "fee_zat": signed.FeeZat})
	}
	fmt.Fprintln(stdout, signed.RawTxHex)
	return 0
}

func runColdToHotBroadcast(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("cold-to-hot broadcast", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	var common commonFlags
	common.bind(fs)

	var services servicesFlags
	services.bind(fs)

	var rawHex string
	var rawFile string
	var waitConf int64

	fs.StringVar(&rawHex, "raw-tx-hex", "", "signed raw tx hex")
	fs.StringVar(&rawFile, "raw-tx-file", "", "path to file containing signed raw tx hex")
	fs.Int64Var(&waitConf, "wait-confirmations", 1, "wait for N confirmations (0 = don't wait)")

	if err := fs.Parse(args); err != nil {
		return 2
	}

	raw, err := loadHexInput(rawHex, rawFile)
	if err != nil {
		return writeErr(stdout, stderr, common.jsonOut, string(types.ErrCodeInvalidRequest), err.Error())
	}

	broadcastURL := services.resolvedBroadcastURL()
	if broadcastURL == "" {
		return writeErr(stdout, stderr, common.jsonOut, string(types.ErrCodeInvalidRequest), "broadcast url required (set --broadcast-url or JUNO_BROADCAST_URL)")
	}
	bc, err := junobroadcast.New(broadcastURL)
	if err != nil {
		return writeErr(stdout, stderr, common.jsonOut, string(types.ErrCodeInvalidRequest), err.Error())
	}

	ctx, cancel := context.WithTimeout(context.Background(), 7*time.Minute)
	defer cancel()

	sub, err := bc.Submit(ctx, raw, nil)
	if err != nil {
		return writeErr(stdout, stderr, common.jsonOut, "broadcast_failed", err.Error())
	}

	if waitConf > 0 {
		if _, err := bc.WaitForConfirmations(ctx, sub.TxID, waitConf); err != nil {
			return writeErrWithDetails(stdout, stderr, common.jsonOut, "confirm_wait_failed", err.Error(), map[string]any{"txid": sub.TxID})
		}
	}

	if common.jsonOut {
		return writeOK(stdout, true, map[string]any{"txid": sub.TxID})
	}
	fmt.Fprintln(stdout, sub.TxID)
	return 0
}

func loadTxPlan(path string) (types.TxPlan, error) {
	var r io.Reader
	if path == "-" {
		r = os.Stdin
	} else {
		b, err := os.ReadFile(path)
		if err != nil {
			return types.TxPlan{}, err
		}
		r = bytes.NewReader(b)
	}
	var plan types.TxPlan
	if err := json.NewDecoder(r).Decode(&plan); err != nil {
		return types.TxPlan{}, errors.New("invalid txplan json")
	}
	return plan, nil
}

func loadHexInput(rawHex, rawFile string) (string, error) {
	var sources int
	if strings.TrimSpace(rawHex) != "" {
		sources++
	}
	if strings.TrimSpace(rawFile) != "" {
		sources++
	}
	if sources == 0 {
		return "", errors.New("raw tx hex is required (use --raw-tx-hex or --raw-tx-file)")
	}
	if sources > 1 {
		return "", errors.New("input source conflict (use only one of --raw-tx-hex, --raw-tx-file)")
	}
	if strings.TrimSpace(rawHex) != "" {
		return strings.TrimSpace(rawHex), nil
	}

	b, err := os.ReadFile(strings.TrimSpace(rawFile))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}
