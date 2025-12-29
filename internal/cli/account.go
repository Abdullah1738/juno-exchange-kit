package cli

import (
	"context"
	"database/sql"
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
	"github.com/Abdullah1738/juno-exchange-kit/internal/store"
	"github.com/Abdullah1738/juno-sdk-go/junocashd"
	"github.com/Abdullah1738/juno-sdk-go/junoscan"
)

func runAccount(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "account: missing subcommand")
		return 2
	}
	switch args[0] {
	case "create":
		return runAccountCreate(args[1:], stdout, stderr)
	case "deposit-address":
		return runAccountDepositAddress(args[1:], stdout, stderr)
	case "balance":
		return runAccountBalance(args[1:], stdout, stderr)
	case "wait-deposit":
		return runAccountWaitDeposit(args[1:], stdout, stderr)
	case "list":
		return runAccountList(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "account: unknown subcommand: %s\n", args[0])
		return 2
	}
}

func runAccountCreate(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("account create", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	var common commonFlags
	common.bind(fs)

	if err := fs.Parse(args); err != nil {
		return 2
	}

	dataDir, err := common.resolvedDataDir()
	if err != nil {
		return writeErr(stdout, stderr, common.jsonOut, "invalid_request", err.Error())
	}
	s, cleanup, err := openStore(dataDir)
	if err != nil {
		return writeErr(stdout, stderr, common.jsonOut, "io_error", err.Error())
	}
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	accountID, err := s.CreateAccount(ctx)
	if err != nil {
		return writeErr(stdout, stderr, common.jsonOut, "db_error", err.Error())
	}

	if common.jsonOut {
		return writeOK(stdout, true, map[string]any{"account_id": accountID})
	}
	fmt.Fprintln(stdout, accountID)
	return 0
}

func runAccountDepositAddress(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("account deposit-address", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	var common commonFlags
	common.bind(fs)

	args = reorderFlagArgs(fs, args)
	if err := fs.Parse(args); err != nil {
		return 2
	}

	if fs.NArg() != 1 {
		return writeErr(stdout, stderr, common.jsonOut, "invalid_request", "account_id is required")
	}
	accountID := fs.Arg(0)

	dataDir, err := common.resolvedDataDir()
	if err != nil {
		return writeErr(stdout, stderr, common.jsonOut, "invalid_request", err.Error())
	}
	s, cleanup, err := openStore(dataDir)
	if err != nil {
		return writeErr(stdout, stderr, common.jsonOut, "io_error", err.Error())
	}
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	uaHRP, ok, err := s.Meta(ctx, "ua_hrp")
	if err != nil {
		return writeErr(stdout, stderr, common.jsonOut, "db_error", err.Error())
	}
	if !ok || strings.TrimSpace(uaHRP) == "" {
		return writeErr(stdout, stderr, common.jsonOut, "invalid_request", "not initialized (run `init`)")
	}

	w, ok, err := s.Wallet(ctx, "hot")
	if err != nil {
		return writeErr(stdout, stderr, common.jsonOut, "db_error", err.Error())
	}
	if !ok {
		return writeErr(stdout, stderr, common.jsonOut, "invalid_request", "hot wallet missing (run `init`)")
	}

	deriver := keysffi.New()
	addr, err := s.GetOrAssignDepositAddress(ctx, accountID, "hot", func(index uint32) (string, error) {
		return deriver.AddressFromUFVK(w.UFVK, uaHRP, keys.ScopeExternal, index)
	})
	if err != nil {
		return writeErr(stdout, stderr, common.jsonOut, "db_error", err.Error())
	}
	if common.jsonOut {
		return writeOK(stdout, true, map[string]any{"account_id": accountID, "address": addr})
	}
	fmt.Fprintln(stdout, addr)
	return 0
}

func runAccountBalance(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("account balance", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	var common commonFlags
	common.bind(fs)

	args = reorderFlagArgs(fs, args)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		return writeErr(stdout, stderr, common.jsonOut, "invalid_request", "account_id is required")
	}
	accountID := strings.TrimSpace(fs.Arg(0))

	dataDir, err := common.resolvedDataDir()
	if err != nil {
		return writeErr(stdout, stderr, common.jsonOut, "invalid_request", err.Error())
	}
	s, cleanup, err := openStore(dataDir)
	if err != nil {
		return writeErr(stdout, stderr, common.jsonOut, "io_error", err.Error())
	}
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	bal, err := s.AccountBalance(ctx, accountID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return writeErr(stdout, stderr, common.jsonOut, "invalid_request", "account not found")
		}
		return writeErr(stdout, stderr, common.jsonOut, "db_error", err.Error())
	}

	if common.jsonOut {
		pending, err := s.PendingDepositsSummary(ctx, &accountID)
		if err != nil {
			return writeErr(stdout, stderr, true, "db_error", err.Error())
		}
		return writeOK(stdout, true, map[string]any{
			"account_id":            accountID,
			"balance_zat":           bal,
			"pending_deposits_zat":  pending.AmountZat,
			"pending_deposit_count": pending.Count,
		})
	}
	fmt.Fprintln(stdout, bal)
	return 0
}

func runAccountWaitDeposit(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("account wait-deposit", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	var common commonFlags
	common.bind(fs)

	var rpc rpcFlags
	rpc.bind(fs)

	var services servicesFlags
	services.bind(fs)

	var timeout time.Duration
	var poll time.Duration
	var minBalanceStr string
	var lookback time.Duration

	fs.DurationVar(&timeout, "timeout", 30*time.Minute, "max wait time")
	fs.DurationVar(&poll, "poll", 2*time.Second, "poll interval")
	fs.StringVar(&minBalanceStr, "min-balance-zat", "", "wait until account balance >= this (zatoshis); default: current+1")
	fs.DurationVar(&lookback, "lookback", 1*time.Hour, "print recent deposit updates from this lookback window")

	args = reorderFlagArgs(fs, args)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		return writeErr(stdout, stderr, common.jsonOut, "invalid_request", "account_id is required")
	}
	accountID := strings.TrimSpace(fs.Arg(0))

	if timeout <= 0 {
		return writeErr(stdout, stderr, common.jsonOut, "invalid_request", "timeout must be > 0")
	}
	if poll <= 0 {
		return writeErr(stdout, stderr, common.jsonOut, "invalid_request", "poll must be > 0")
	}
	if lookback < 0 {
		return writeErr(stdout, stderr, common.jsonOut, "invalid_request", "lookback must be >= 0")
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

	rpcURL, rpcUser, rpcPass, rpcErr := rpc.resolved()
	var rpcClient *junocashd.Client
	if rpcErr == nil {
		rpcClient = junocashd.New(rpcURL, rpcUser, rpcPass)
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	startBal, err := st.AccountBalance(ctx, accountID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return writeErr(stdout, stderr, common.jsonOut, "invalid_request", "account not found")
		}
		return writeErr(stdout, stderr, common.jsonOut, "db_error", err.Error())
	}

	targetBal := startBal + 1
	if minBalanceStr != "" {
		u, err := strconv.ParseUint(strings.TrimSpace(minBalanceStr), 10, 64)
		if err != nil {
			return writeErr(stdout, stderr, common.jsonOut, "invalid_request", "min-balance-zat must be an integer")
		}
		if u > uint64(math.MaxInt64) {
			return writeErr(stdout, stderr, common.jsonOut, "invalid_request", "min-balance-zat too large")
		}
		targetBal = int64(u)
	}
	if targetBal < 0 {
		targetBal = startBal + 1
	}

	since := time.Now().Add(-lookback)
	seen := make(map[string]string)

	for {
		bal, err := st.AccountBalance(ctx, accountID)
		if err != nil {
			return writeErr(stdout, stderr, common.jsonOut, "db_error", err.Error())
		}
		if bal >= targetBal {
			if common.jsonOut {
				return writeOK(stdout, true, map[string]any{
					"account_id":         accountID,
					"start_balance_zat":  startBal,
					"target_balance_zat": targetBal,
					"balance_zat":        bal,
					"delta_zat":          bal - startBal,
				})
			}
			fmt.Fprintln(stdout, bal)
			return 0
		}

		syncCtx, syncCancel := context.WithTimeout(ctx, 60*time.Second)
		_, _ = syncWallet(syncCtx, st, sc, "hot", io.Discard, true)
		syncCancel()

		printCtx, printCancel := context.WithTimeout(ctx, 5*time.Second)
		tipHeight := int64(0)
		if rpcClient != nil {
			if info, err := rpcClient.GetBlockchainInfo(printCtx); err == nil {
				tipHeight = info.Blocks
			}
		}
		_ = printDepositUpdates(printCtx, stdout, common.jsonOut, st, accountID, since.Unix(), tipHeight, seen)
		printCancel()

		select {
		case <-ctx.Done():
			return writeErr(stdout, stderr, common.jsonOut, "timeout", "deposit wait timeout")
		case <-time.After(poll):
		}
	}
}

func printDepositUpdates(ctx context.Context, stdout io.Writer, jsonOut bool, st *store.Store, accountID string, sinceUnix int64, tipHeight int64, seen map[string]string) error {
	if jsonOut {
		return nil
	}
	if st == nil {
		return errors.New("nil store")
	}
	if seen == nil {
		return errors.New("nil seen map")
	}

	recs, err := st.ListAccountDepositsSince(ctx, accountID, sinceUnix, 500)
	if err != nil {
		return err
	}
	now := time.Now()

	for _, r := range recs {
		key := r.WalletID + ":" + r.TxID + ":" + strconv.FormatUint(uint64(r.ActionIndex), 10)
		prev := seen[key]
		cur := string(r.Status)
		if prev == cur {
			continue
		}
		seen[key] = cur

		amount := formatJUNO(r.AmountZat)
		ago := humanAgo(now.Sub(r.UpdatedAt))
		confStr := ""
		if tipHeight > 0 && r.Height > 0 && tipHeight >= r.Height {
			conf := tipHeight - r.Height + 1
			if conf < 0 {
				conf = 0
			}
			confStr = fmt.Sprintf(" conf=%d", conf)
		}

		switch r.Status {
		case store.DepositStatusDetected, store.DepositStatusUnconfirmed:
			fmt.Fprintf(stdout, "NEW_PENDING_DEPOSIT %s JUNO%s %s\n", amount, confStr, ago)
		case store.DepositStatusConfirmed:
			fmt.Fprintf(stdout, "NEW_CONFIRMED_DEPOSIT %s JUNO%s %s\n", amount, confStr, ago)
		case store.DepositStatusOrphaned:
			fmt.Fprintf(stdout, "DEPOSIT_ORPHANED %s JUNO%s %s\n", amount, confStr, ago)
		default:
			// ignore
		}
	}
	return nil
}

func formatJUNO(amountZat int64) string {
	neg := amountZat < 0
	if neg {
		amountZat = -amountZat
	}

	const unit = int64(100_000_000)
	whole := amountZat / unit
	frac := amountZat % unit
	s := fmt.Sprintf("%d.%08d", whole, frac)
	s = strings.TrimRight(s, "0")
	s = strings.TrimRight(s, ".")
	if s == "" {
		s = "0"
	}
	if neg {
		return "-" + s
	}
	return s
}

func humanAgo(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	if d < time.Second {
		return "now"
	}
	if d < time.Minute {
		sec := int(d.Seconds())
		if sec == 1 {
			return "1 second ago"
		}
		return fmt.Sprintf("%d seconds ago", sec)
	}
	if d < time.Hour {
		min := int(d.Minutes())
		if min == 1 {
			return "1 minute ago"
		}
		return fmt.Sprintf("%d minutes ago", min)
	}
	hr := int(d.Hours())
	if hr == 1 {
		return "1 hour ago"
	}
	return fmt.Sprintf("%d hours ago", hr)
}

func runAccountList(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("account list", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	var common commonFlags
	common.bind(fs)

	var limit int
	var offset int
	var minBalanceStr string
	fs.IntVar(&limit, "limit", 100, "max rows (<= 1000)")
	fs.IntVar(&offset, "offset", 0, "offset (>= 0)")
	fs.StringVar(&minBalanceStr, "min-balance-zat", "", "optional filter: only accounts with balance >= this (zatoshis)")

	args = reorderFlagArgs(fs, args)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		return writeErr(stdout, stderr, common.jsonOut, "invalid_request", "account list takes no positional args")
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

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var minBalancePtr *int64
	if strings.TrimSpace(minBalanceStr) != "" {
		u, err := strconv.ParseUint(strings.TrimSpace(minBalanceStr), 10, 64)
		if err != nil {
			return writeErr(stdout, stderr, common.jsonOut, "invalid_request", "min-balance-zat must be an integer")
		}
		if u > uint64(math.MaxInt64) {
			return writeErr(stdout, stderr, common.jsonOut, "invalid_request", "min-balance-zat too large")
		}
		v := int64(u)
		minBalancePtr = &v
	}

	rows, err := st.ListAccounts(ctx, limit, offset, minBalancePtr)
	if err != nil {
		return writeErr(stdout, stderr, common.jsonOut, "db_error", err.Error())
	}

	if common.jsonOut {
		type row struct {
			AccountID  string    `json:"account_id"`
			BalanceZat int64     `json:"balance_zat"`
			CreatedAt  time.Time `json:"created_at"`
			UpdatedAt  time.Time `json:"updated_at"`
		}
		out := make([]row, 0, len(rows))
		for _, r := range rows {
			out = append(out, row{
				AccountID:  r.AccountID,
				BalanceZat: r.BalanceZat,
				CreatedAt:  r.CreatedAt,
				UpdatedAt:  r.UpdatedAt,
			})
		}
		return writeOK(stdout, true, map[string]any{
			"accounts":    out,
			"count":       len(out),
			"limit":       limit,
			"offset":      offset,
			"next_offset": offset + len(out),
		})
	}

	for _, r := range rows {
		fmt.Fprintf(stdout, "%s %d\n", r.AccountID, r.BalanceZat)
	}
	return 0
}
