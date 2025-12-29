package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
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

func runWithdraw(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("withdraw", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	var common commonFlags
	common.bind(fs)

	var rpc rpcFlags
	rpc.bind(fs)

	var services servicesFlags
	services.bind(fs)

	var accountID string
	var to string
	var amountStr string
	var minconf int64
	var expiryOffset uint
	var waitConf int64
	var txsignBin string

	fs.StringVar(&accountID, "account", "", "account id")
	fs.StringVar(&to, "to", "", "destination unified address (j...)")
	fs.StringVar(&amountStr, "amount-zat", "", "amount in zatoshis")
	fs.Int64Var(&minconf, "minconf", 1, "minimum confirmations")
	fs.UintVar(&expiryOffset, "expiry-offset", 40, "expiry height offset from chain tip")
	fs.Int64Var(&waitConf, "wait-confirmations", 1, "wait for N confirmations (0 = don't wait)")
	fs.StringVar(&txsignBin, "txsign-bin", "", "path to juno-txsign binary")

	if err := fs.Parse(args); err != nil {
		return 2
	}

	accountID = strings.TrimSpace(accountID)
	to = strings.TrimSpace(to)
	amountStr = strings.TrimSpace(amountStr)
	if accountID == "" || to == "" || amountStr == "" {
		return writeErr(stdout, stderr, common.jsonOut, string(types.ErrCodeInvalidRequest), "account, to, and amount-zat are required")
	}
	amountU64, err := strconv.ParseUint(amountStr, 10, 64)
	if err != nil || amountU64 == 0 {
		return writeErr(stdout, stderr, common.jsonOut, string(types.ErrCodeInvalidRequest), "amount-zat must be a positive integer")
	}
	if amountU64 > uint64(math.MaxInt64) {
		return writeErr(stdout, stderr, common.jsonOut, string(types.ErrCodeInvalidRequest), "amount-zat too large")
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
	if err != nil {
		return writeErr(stdout, stderr, common.jsonOut, "db_error", err.Error())
	}
	if !ok || strings.TrimSpace(uaHRP) == "" {
		return writeErr(stdout, stderr, common.jsonOut, string(types.ErrCodeInvalidRequest), "not initialized (run `init`)")
	}

	coinTypeStr, ok, err := st.Meta(ctx, "coin_type")
	if err != nil {
		return writeErr(stdout, stderr, common.jsonOut, "db_error", err.Error())
	}
	if !ok {
		return writeErr(stdout, stderr, common.jsonOut, string(types.ErrCodeInvalidRequest), "missing coin_type (run `init`)")
	}
	coinTypeU64, err := strconv.ParseUint(strings.TrimSpace(coinTypeStr), 10, 32)
	if err != nil || coinTypeU64 == 0 {
		return writeErr(stdout, stderr, common.jsonOut, string(types.ErrCodeInvalidRequest), "invalid coin_type")
	}
	coinType := uint32(coinTypeU64)

	wallet, ok, err := st.Wallet(ctx, "hot")
	if err != nil {
		return writeErr(stdout, stderr, common.jsonOut, "db_error", err.Error())
	}
	if !ok {
		return writeErr(stdout, stderr, common.jsonOut, string(types.ErrCodeInvalidRequest), "hot wallet missing (run `init`)")
	}

	// Fail fast for insufficient account balance (fee is checked after plan build).
	accountBal, err := st.AccountBalance(ctx, accountID)
	if err != nil {
		return writeErr(stdout, stderr, common.jsonOut, "db_error", err.Error())
	}
	if accountBal < int64(amountU64) {
		return writeErrWithDetails(stdout, stderr, common.jsonOut, string(types.ErrCodeInsufficientBalance), "INSUFFICIENT_BALANCE", map[string]any{
			"account_id":          accountID,
			"account_balance_zat": accountBal,
			"requested_amount_zat": func() int64 {
				if amountU64 > uint64(math.MaxInt64) {
					return math.MaxInt64
				}
				return int64(amountU64)
			}(),
		})
	}

	rpcURL, rpcUser, rpcPass, err := rpc.resolved()
	if err != nil {
		return writeErr(stdout, stderr, common.jsonOut, string(types.ErrCodeInvalidRequest), err.Error())
	}
	scanURL := services.resolvedScanURL()
	if scanURL == "" {
		return writeErr(stdout, stderr, common.jsonOut, string(types.ErrCodeInvalidRequest), "scan url required (set --scan-url or JUNO_SCAN_URL)")
	}
	broadcastURL := services.resolvedBroadcastURL()
	if broadcastURL == "" {
		return writeErr(stdout, stderr, common.jsonOut, string(types.ErrCodeInvalidRequest), "broadcast url required (set --broadcast-url or JUNO_BROADCAST_URL)")
	}

	rpcClient := junocashd.New(rpcURL, rpcUser, rpcPass)
	sc, err := junoscan.New(scanURL)
	if err != nil {
		return writeErr(stdout, stderr, common.jsonOut, string(types.ErrCodeInvalidRequest), err.Error())
	}
	bc, err := junobroadcast.New(broadcastURL)
	if err != nil {
		return writeErr(stdout, stderr, common.jsonOut, string(types.ErrCodeInvalidRequest), err.Error())
	}

	// Ensure wallet is registered with the scanner.
	_ = sc.UpsertWallet(ctx, "hot", wallet.UFVK)

	deriver := keysffi.New()
	changeAddr, err := st.NextInternalAddress(ctx, "hot", func(index uint32) (string, error) {
		return deriver.AddressFromUFVK(wallet.UFVK, uaHRP, keys.ScopeInternal, index)
	})
	if err != nil {
		if errors.Is(err, keys.ErrUnavailable) {
			return writeErr(stdout, stderr, common.jsonOut, "keys_unavailable", "key derivation unavailable (build with CGO and run `make rust-build`)")
		}
		return writeErr(stdout, stderr, common.jsonOut, "db_error", err.Error())
	}

	plan, fee, err := txplan.PlanWithdrawal(ctx, txplan.SendConfig{
		RPC:              rpcClient,
		Scan:             sc,
		Wallet:           "hot",
		CoinType:         coinType,
		Account:          0,
		MinConfirmations: minconf,
		ExpiryOffset:     uint32(expiryOffset),
	}, []txplan.Output{{ToAddress: to, AmountZat: amountU64}}, changeAddr)
	if err != nil {
		var ce types.CodedError
		if errors.As(err, &ce) && ce.Code == types.ErrCodeNoLiquidityInHot {
			return writeErr(stdout, stderr, common.jsonOut, string(types.ErrCodeNoLiquidityInHot), "NO_LIQUIDITY_IN_HOT")
		}
		return writeErr(stdout, stderr, common.jsonOut, string(types.ErrCodeInvalidRequest), err.Error())
	}

	total := int64(amountU64) + int64(fee)
	if total < int64(amountU64) {
		return writeErr(stdout, stderr, common.jsonOut, string(types.ErrCodeInvalidRequest), "amount overflow")
	}
	if accountBal < total {
		maxAmount := int64(0)
		if accountBal > 0 && accountBal > int64(fee) {
			maxAmount = accountBal - int64(fee)
		}
		return writeErrWithDetails(stdout, stderr, common.jsonOut, string(types.ErrCodeInsufficientBalance), "INSUFFICIENT_BALANCE", map[string]any{
			"account_id":           accountID,
			"account_balance_zat":  accountBal,
			"requested_amount_zat": int64(amountU64),
			"fee_zat":              int64(fee),
			"max_amount_zat":       maxAmount,
		})
	}

	if strings.TrimSpace(txsignBin) == "" {
		txsignBin = txsignBinFromEnv()
	}
	signed, err := signTxPlan(ctx, txsignBin, wallet.SeedPath, plan)
	if err != nil {
		return writeErr(stdout, stderr, common.jsonOut, "sign_failed", err.Error())
	}
	feeI64, err := strconv.ParseInt(strings.TrimSpace(signed.FeeZat), 10, 64)
	if err != nil || feeI64 < 0 {
		return writeErr(stdout, stderr, common.jsonOut, "sign_failed", "invalid fee returned by signer")
	}

	sub, err := bc.Submit(ctx, signed.RawTxHex, nil)
	if err != nil {
		return writeErr(stdout, stderr, common.jsonOut, "broadcast_failed", err.Error())
	}

	if _, err := st.CreateWithdrawalAndDebit(ctx, accountID, "hot", to, int64(amountU64), feeI64, sub.TxID); err != nil {
		// At this point the tx is already broadcast; surface a clear error.
		return writeErr(stdout, stderr, common.jsonOut, string(types.ErrCodeInvalidRequest), err.Error())
	}

	if waitConf > 0 {
		if _, err := bc.WaitForConfirmations(ctx, sub.TxID, waitConf); err != nil {
			return writeErrWithDetails(stdout, stderr, common.jsonOut, "confirm_wait_failed", err.Error(), map[string]any{"txid": sub.TxID})
		}
	}

	if common.jsonOut {
		return writeOK(stdout, true, map[string]any{
			"txid":      sub.TxID,
			"fee_zat":   feeI64,
			"wallet_id": "hot",
		})
	}
	fmt.Fprintln(stdout, sub.TxID)
	return 0
}
