package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"strings"
	"time"

	"github.com/Abdullah1738/juno-exchange-kit/internal/keys"
	keysffi "github.com/Abdullah1738/juno-exchange-kit/internal/keys/ffi"
	"github.com/Abdullah1738/juno-sdk-go/junocashd"
	"github.com/Abdullah1738/juno-sdk-go/junoscan"
)

func runWallet(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "wallet: missing subcommand")
		return 2
	}
	switch args[0] {
	case "balance":
		return runWalletBalance(args[1:], stdout, stderr)
	case "addresses":
		return runWalletAddresses(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "wallet: unknown subcommand: %s\n", args[0])
		return 2
	}
}

func runWalletBalance(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("wallet balance", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	var common commonFlags
	common.bind(fs)
	var rpc rpcFlags
	rpc.bind(fs)
	var services servicesFlags
	services.bind(fs)

	var minconf int64
	fs.Int64Var(&minconf, "minconf", 1, "minimum confirmations (0 = include unconfirmed)")

	args = reorderFlagArgs(fs, args)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		return writeErr(stdout, stderr, common.jsonOut, "invalid_request", "wallet_id is required")
	}
	walletID := strings.TrimSpace(fs.Arg(0))
	if walletID == "" {
		return writeErr(stdout, stderr, common.jsonOut, "invalid_request", "wallet_id is required")
	}
	if minconf < 0 {
		return writeErr(stdout, stderr, common.jsonOut, "invalid_request", "minconf must be >= 0")
	}

	scanURL := services.resolvedScanURL()
	if scanURL == "" {
		return writeErr(stdout, stderr, common.jsonOut, "invalid_request", "scan url required (set --scan-url or JUNO_SCAN_URL)")
	}
	sc, err := junoscan.New(scanURL)
	if err != nil {
		return writeErr(stdout, stderr, common.jsonOut, "invalid_request", err.Error())
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	raw, err := sc.ListWalletNotes(ctx, walletID, true)
	if err != nil {
		return writeErr(stdout, stderr, common.jsonOut, "scan_error", err.Error())
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

	var total int64
	var spendable int64
	unspentNotes := 0
	spendableNotes := 0
	for _, n := range raw {
		if n.ValueZat <= 0 {
			continue
		}
		if n.ValueZat > int64(math.MaxInt64-total) {
			return writeErr(stdout, stderr, common.jsonOut, "internal_error", "wallet balance overflow")
		}
		total += n.ValueZat
		unspentNotes++

		if minconf == 0 {
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
				return writeErr(stdout, stderr, common.jsonOut, "internal_error", "wallet balance overflow")
			}
			spendable += n.ValueZat
			spendableNotes++
		}
	}

	if common.jsonOut {
		return writeOK(stdout, true, map[string]any{
			"wallet_id":          walletID,
			"minconf":            minconf,
			"tip_height":         tipHeight,
			"total_unspent_zat":  total,
			"spendable_zat":      spendable,
			"unspent_notes":      unspentNotes,
			"spendable_notes":    spendableNotes,
			"scan_url":           scanURL,
			"computed_at_utc":    time.Now().UTC().Format(time.RFC3339),
			"includes_unconfirmed": minconf == 0,
		})
	}

	fmt.Fprintf(stdout, "%s total_unspent_zat=%d spendable_zat=%d unspent_notes=%d spendable_notes=%d minconf=%d tip_height=%d\n", walletID, total, spendable, unspentNotes, spendableNotes, minconf, tipHeight)
	return 0
}

func runWalletAddresses(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("wallet addresses", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	var common commonFlags
	common.bind(fs)

	var scope string
	var accountID string
	var limit int
	var offset int
	fs.StringVar(&scope, "scope", "external", "external|internal|all")
	fs.StringVar(&accountID, "account", "", "optional account id filter (external only)")
	fs.IntVar(&limit, "limit", 100, "max rows (<= 1000)")
	fs.IntVar(&offset, "offset", 0, "offset (>= 0)")

	args = reorderFlagArgs(fs, args)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		return writeErr(stdout, stderr, common.jsonOut, "invalid_request", "wallet_id is required")
	}
	walletID := strings.TrimSpace(fs.Arg(0))
	if walletID == "" {
		return writeErr(stdout, stderr, common.jsonOut, "invalid_request", "wallet_id is required")
	}

	scope = strings.ToLower(strings.TrimSpace(scope))
	if scope == "" {
		scope = "external"
	}
	if scope != "external" && scope != "internal" && scope != "all" {
		return writeErr(stdout, stderr, common.jsonOut, "invalid_request", "scope must be external|internal|all")
	}
	if limit <= 0 || limit > 1000 {
		return writeErr(stdout, stderr, common.jsonOut, "invalid_request", "limit must be between 1 and 1000")
	}
	if offset < 0 {
		return writeErr(stdout, stderr, common.jsonOut, "invalid_request", "offset must be >= 0")
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

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	uaHRP, ok, err := st.Meta(ctx, "ua_hrp")
	if err != nil {
		return writeErr(stdout, stderr, common.jsonOut, "db_error", err.Error())
	}
	if !ok || strings.TrimSpace(uaHRP) == "" {
		return writeErr(stdout, stderr, common.jsonOut, "invalid_request", "not initialized (run `init`)")
	}

	w, ok, err := st.Wallet(ctx, walletID)
	if err != nil {
		return writeErr(stdout, stderr, common.jsonOut, "db_error", err.Error())
	}
	if !ok {
		return writeErr(stdout, stderr, common.jsonOut, "invalid_request", "wallet missing (run `init`)")
	}

	type row struct {
		Scope     string     `json:"scope"`
		Index     uint32     `json:"index"`
		Address   string     `json:"address"`
		AccountID *string    `json:"account_id,omitempty"`
		CreatedAt *time.Time `json:"created_at,omitempty"`
	}
	out := make([]row, 0, limit)

	if scope == "external" || scope == "all" {
		var accountPtr *string
		if strings.TrimSpace(accountID) != "" {
			v := strings.TrimSpace(accountID)
			accountPtr = &v
		}
		addrs, err := st.ListDepositAddresses(ctx, walletID, accountPtr, limit, offset)
		if err != nil {
			return writeErr(stdout, stderr, common.jsonOut, "db_error", err.Error())
		}
		for _, a := range addrs {
			aid := a.AccountID
			created := a.CreatedAt
			out = append(out, row{
				Scope:     "external",
				Index:     a.Index,
				Address:   a.Address,
				AccountID: &aid,
				CreatedAt: &created,
			})
		}
		if scope == "external" {
			if common.jsonOut {
				return writeOK(stdout, true, map[string]any{
					"wallet_id": walletID,
					"scope":     scope,
					"count":     len(out),
					"addresses": out,
				})
			}
			for _, r := range out {
				fmt.Fprintf(stdout, "%s %d %s %s\n", r.Scope, r.Index, *r.AccountID, r.Address)
			}
			return 0
		}
	}

	if scope == "internal" || scope == "all" {
		deriver := keysffi.New()
		start := uint32(offset)
		if offset > int(w.NextInternalIndex) {
			start = w.NextInternalIndex
		}
		end := w.NextInternalIndex
		if remain := int(end) - int(start); remain > limit {
			end = start + uint32(limit)
		}
		for i := start; i < end; i++ {
			addr, err := deriver.AddressFromUFVK(w.UFVK, uaHRP, keys.ScopeInternal, i)
			if err != nil {
				if errors.Is(err, keys.ErrUnavailable) {
					return writeErr(stdout, stderr, common.jsonOut, "keys_unavailable", "key derivation unavailable (build with CGO and run `make rust-build`)")
				}
				return writeErr(stdout, stderr, common.jsonOut, "keys_error", err.Error())
			}
			out = append(out, row{
				Scope:   "internal",
				Index:   i,
				Address: addr,
			})
		}
	}

	if common.jsonOut {
		return writeOK(stdout, true, map[string]any{
			"wallet_id":  walletID,
			"scope":      scope,
			"count":      len(out),
			"addresses":  out,
			"ua_hrp":     uaHRP,
			"next_internal_index":  w.NextInternalIndex,
			"next_external_index":  w.NextExternalIndex,
		})
	}

	for _, r := range out {
		if r.AccountID != nil {
			fmt.Fprintf(stdout, "%s %d %s %s\n", r.Scope, r.Index, *r.AccountID, r.Address)
			continue
		}
		fmt.Fprintf(stdout, "%s %d %s\n", r.Scope, r.Index, r.Address)
	}
	return 0
}
