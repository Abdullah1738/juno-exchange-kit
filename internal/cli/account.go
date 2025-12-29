package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/Abdullah1738/juno-exchange-kit/internal/keys"
	keysffi "github.com/Abdullah1738/juno-exchange-kit/internal/keys/ffi"
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
		return writeErr(stdout, stderr, common.jsonOut, "db_error", err.Error())
	}

	if common.jsonOut {
		return writeOK(stdout, true, map[string]any{"account_id": accountID, "balance_zat": bal})
	}
	fmt.Fprintln(stdout, bal)
	return 0
}

func runAccountWaitDeposit(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("account wait-deposit", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	var common commonFlags
	common.bind(fs)

	var services servicesFlags
	services.bind(fs)

	var timeout time.Duration
	var poll time.Duration
	var minBalanceStr string

	fs.DurationVar(&timeout, "timeout", 30*time.Minute, "max wait time")
	fs.DurationVar(&poll, "poll", 2*time.Second, "poll interval")
	fs.StringVar(&minBalanceStr, "min-balance-zat", "", "wait until account balance >= this (zatoshis); default: current+1")

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

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	startBal, err := st.AccountBalance(ctx, accountID)
	if err != nil {
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

		select {
		case <-ctx.Done():
			return writeErr(stdout, stderr, common.jsonOut, "timeout", "deposit wait timeout")
		case <-time.After(poll):
		}
	}
}
