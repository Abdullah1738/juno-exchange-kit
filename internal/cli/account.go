package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"time"
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

	addr, err := s.GetOrAssignDepositAddress(ctx, accountID)
	if err != nil {
		return writeErr(stdout, stderr, common.jsonOut, "db_error", err.Error())
	}
	if common.jsonOut {
		return writeOK(stdout, true, map[string]any{"account_id": accountID, "address": addr})
	}
	fmt.Fprintln(stdout, addr)
	return 0
}

