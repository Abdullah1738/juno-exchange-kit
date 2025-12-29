package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/Abdullah1738/juno-exchange-kit/internal/app"
	"github.com/Abdullah1738/juno-exchange-kit/internal/store"
)

func Run(args []string) int {
	return RunWithIO(args, os.Stdout, os.Stderr)
}

func RunWithIO(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		writeUsage(stdout)
		return 2
	}

	switch args[0] {
	case "-h", "--help", "help":
		writeUsage(stdout)
		return 0
	case "version":
		fmt.Fprintln(stdout, app.Name)
		return 0
	case "init":
		return runInit(args[1:], stdout, stderr)
	case "account":
		return runAccount(args[1:], stdout, stderr)
	case "wallet":
		return runWallet(args[1:], stdout, stderr)
	case "balances":
		return runBalances(args[1:], stdout, stderr)
	case "daemon":
		return runDaemon(args[1:], stdout, stderr)
	case "sync":
		return runSync(args[1:], stdout, stderr)
	case "sweep":
		return runSweep(args[1:], stdout, stderr)
	case "withdraw":
		return runWithdraw(args[1:], stdout, stderr)
	case "withdrawals":
		return runWithdrawals(args[1:], stdout, stderr)
	case "cold-to-hot":
		return runColdToHot(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown command: %s\n\n", args[0])
		writeUsage(stderr)
		return 2
	}
}

func writeUsage(w io.Writer) {
	fmt.Fprintln(w, app.Name)
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Local CLI exchange harness for Juno Cash exchange integrations.")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Usage:")
	fmt.Fprintln(w, "  juno-exchange-kit init [--data-dir <dir>] [--json]")
	fmt.Fprintln(w, "  juno-exchange-kit account create [--data-dir <dir>] [--json]")
	fmt.Fprintln(w, "  juno-exchange-kit account deposit-address <account_id> [--data-dir <dir>] [--json]")
	fmt.Fprintln(w, "  juno-exchange-kit account balance <account_id> [--data-dir <dir>] [--json]")
	fmt.Fprintln(w, "  juno-exchange-kit account wait-deposit <account_id> [--min-balance-zat <n>] [--timeout <dur>] [--poll <dur>] [--data-dir <dir>] [--json]")
	fmt.Fprintln(w, "  juno-exchange-kit account list [--min-balance-zat <n>] [--limit <n>] [--offset <n>] [--data-dir <dir>] [--json]")
	fmt.Fprintln(w, "  juno-exchange-kit wallet balance <wallet_id> [--minconf <n>] [--data-dir <dir>] [--json]")
	fmt.Fprintln(w, "  juno-exchange-kit wallet addresses <wallet_id> [--scope external|internal|all] [--account <id>] [--limit <n>] [--offset <n>] [--data-dir <dir>] [--json]")
	fmt.Fprintln(w, "  juno-exchange-kit balances [--minconf <n>] [--sync] [--data-dir <dir>] [--json]")
	fmt.Fprintln(w, "  juno-exchange-kit daemon [--poll <dur>] [--once] [--data-dir <dir>] [--json]  # foreground")
	fmt.Fprintln(w, "  juno-exchange-kit daemon start [--poll <dur>] [--data-dir <dir>] [--json]      # background")
	fmt.Fprintln(w, "  juno-exchange-kit daemon status [--data-dir <dir>] [--json]")
	fmt.Fprintln(w, "  juno-exchange-kit daemon stop [--data-dir <dir>] [--json]")
	fmt.Fprintln(w, "  juno-exchange-kit sync [--data-dir <dir>] [--json]")
	fmt.Fprintln(w, "  juno-exchange-kit sweep consolidate [--data-dir <dir>] [--json]")
	fmt.Fprintln(w, "  juno-exchange-kit sweep to-cold [--data-dir <dir>] [--json]")
	fmt.Fprintln(w, "  juno-exchange-kit withdraw --account <id> --to <j...> --amount-zat <n> [--data-dir <dir>] [--json]")
	fmt.Fprintln(w, "  juno-exchange-kit withdrawals list [--account <id>] [--data-dir <dir>] [--json]")
	fmt.Fprintln(w, "  juno-exchange-kit cold-to-hot plan --amount-zat <n> [--out <path>] [--data-dir <dir>] [--json]")
	fmt.Fprintln(w, "  juno-exchange-kit cold-to-hot sign --txplan <path|-> [--out <path>] [--data-dir <dir>] [--json]")
	fmt.Fprintln(w, "  juno-exchange-kit cold-to-hot broadcast --raw-tx-hex <hex> [--wait-confirmations <n>] [--data-dir <dir>] [--json]")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Env:")
	fmt.Fprintln(w, "  JUNO_EXCHANGE_KIT_DATA_DIR")
	fmt.Fprintln(w, "  JUNO_RPC_URL, JUNO_RPC_USER, JUNO_RPC_PASS")
	fmt.Fprintln(w, "  JUNO_SCAN_URL, JUNO_BROADCAST_URL")
	fmt.Fprintln(w, "  JUNO_TXSIGN_BIN")
}

type commonFlags struct {
	dataDir string
	jsonOut bool
}

func (c *commonFlags) bind(fs *flag.FlagSet) {
	fs.StringVar(&c.dataDir, "data-dir", "", "data directory")
	fs.BoolVar(&c.jsonOut, "json", false, "JSON output")
}

func (c *commonFlags) resolvedDataDir() (string, error) {
	if strings.TrimSpace(c.dataDir) != "" {
		return strings.TrimSpace(c.dataDir), nil
	}
	if v := strings.TrimSpace(os.Getenv("JUNO_EXCHANGE_KIT_DATA_DIR")); v != "" {
		return v, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("data dir: %w", err)
	}
	return home + string(os.PathSeparator) + ".juno-exchange-kit", nil
}

func openStore(dataDir string) (*store.Store, func(), error) {
	s, err := store.Open(dataDir)
	if err != nil {
		return nil, nil, err
	}
	cleanup := func() { _ = s.Close() }
	return s, cleanup, nil
}

func writeOK(w io.Writer, jsonOut bool, data any) int {
	if jsonOut {
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "data": data})
		return 0
	}
	if data != nil {
		_ = json.NewEncoder(w).Encode(data)
	}
	return 0
}

func writeErr(stdout, stderr io.Writer, jsonOut bool, code, msg string) int {
	if jsonOut {
		_ = json.NewEncoder(stdout).Encode(map[string]any{
			"status": "err",
			"error": map[string]any{
				"code":    code,
				"message": msg,
			},
		})
		return 1
	}
	if msg == "" {
		msg = code
	}
	fmt.Fprintln(stderr, msg)
	return 1
}

func writeErrWithDetails(stdout, stderr io.Writer, jsonOut bool, code, msg string, details map[string]any) int {
	if jsonOut {
		errObj := map[string]any{
			"code":    code,
			"message": msg,
		}
		for k, v := range details {
			if strings.TrimSpace(k) == "" {
				continue
			}
			errObj[k] = v
		}
		_ = json.NewEncoder(stdout).Encode(map[string]any{
			"status": "err",
			"error":  errObj,
		})
		return 1
	}
	if msg == "" {
		msg = code
	}
	if len(details) == 0 {
		fmt.Fprintln(stderr, msg)
		return 1
	}

	extra := make([]string, 0, len(details))
	for k, v := range details {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		extra = append(extra, fmt.Sprintf("%s=%v", k, v))
	}
	sort.Strings(extra)
	fmt.Fprintf(stderr, "%s (%s)\n", msg, strings.Join(extra, " "))
	return 1
}
