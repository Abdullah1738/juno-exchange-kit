package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"
)

func runWithdrawals(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "withdrawals: missing subcommand")
		return 2
	}
	switch args[0] {
	case "list":
		return runWithdrawalsList(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "withdrawals: unknown subcommand: %s\n", args[0])
		return 2
	}
}

func runWithdrawalsList(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("withdrawals list", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	var common commonFlags
	common.bind(fs)

	var accountID string
	var limit int
	fs.StringVar(&accountID, "account", "", "optional account id filter")
	fs.IntVar(&limit, "limit", 50, "max rows")

	if err := fs.Parse(args); err != nil {
		return 2
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

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ws, err := st.ListWithdrawals(ctx, strings.TrimSpace(accountID), limit)
	if err != nil {
		return writeErr(stdout, stderr, common.jsonOut, "db_error", err.Error())
	}

	if common.jsonOut {
		return writeOK(stdout, true, map[string]any{"withdrawals": ws})
	}

	for _, w := range ws {
		txid := ""
		if w.TxID != nil {
			txid = *w.TxID
		}
		fmt.Fprintf(stdout, "%s %s %s %d txid=%s\n", w.CreatedAt.UTC().Format(time.RFC3339), w.ID, w.ToAddress, w.AmountZat, txid)
	}
	return 0
}
