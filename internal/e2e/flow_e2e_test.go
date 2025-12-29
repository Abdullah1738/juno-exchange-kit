//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Abdullah1738/juno-exchange-kit/internal/testutil"
	"github.com/Abdullah1738/juno-sdk-go/junoscan"
)

func TestExchangeKit_DepositSweepColdToHotWithdraw_E2E(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()

	rootTmp := t.TempDir()
	stack, err := testutil.StartStack(ctx, filepath.Join(rootTmp, "stack"), testutil.StartStackConfig{
		UaHRP:         "jregtest",
		Confirmations: 1,
	})
	if err != nil {
		t.Fatalf("StartStack: %v", err)
	}
	defer stack.Close(context.Background())

	txsignBin, err := testutil.EnsureTool(ctx, testutil.ToolSpec{
		EnvVar:      "JUNO_TEST_JUNO_TXSIGN_BIN",
		BinaryName:  "juno-txsign",
		SiblingPath: filepath.Join("..", "juno-txsign", "bin", "juno-txsign"),
		BuildDir:    filepath.Join("..", "juno-txsign"),
	})
	if err != nil {
		t.Fatalf("EnsureTool(juno-txsign): %v", err)
	}

	bin := filepath.Join("..", "..", "bin", "juno-exchange-kit")
	if _, err := os.Stat(bin); err != nil {
		t.Fatalf("missing binary (run `make build`): %v", err)
	}

	dataDir := filepath.Join(rootTmp, "kit")
	env := map[string]string{
		"JUNO_EXCHANGE_KIT_DATA_DIR": dataDir,
		"JUNO_RPC_URL":               stack.Junocashd.RPCURL,
		"JUNO_RPC_USER":              stack.Junocashd.RPCUser,
		"JUNO_RPC_PASS":              stack.Junocashd.RPCPassword,
		"JUNO_SCAN_URL":              stack.ScanURL,
		"JUNO_BROADCAST_URL":         stack.BroadcastURL,
		"JUNO_TXSIGN_BIN":            txsignBin,
	}

	mustRunKitOK(t, ctx, bin, env, nil, "init", "--json")

	var accountResp struct {
		Status string `json:"status"`
		Data   struct {
			AccountID string `json:"account_id"`
		} `json:"data"`
		Error any `json:"error"`
	}
	mustRunKitOKInto(t, ctx, bin, env, nil, &accountResp, "account", "create", "--json")
	if strings.TrimSpace(accountResp.Data.AccountID) == "" {
		t.Fatalf("missing account_id")
	}
	accountID := strings.TrimSpace(accountResp.Data.AccountID)

	var depositAddrResp struct {
		Status string `json:"status"`
		Data   struct {
			AccountID string `json:"account_id"`
			Address   string `json:"address"`
		} `json:"data"`
		Error any `json:"error"`
	}
	mustRunKitOKInto(t, ctx, bin, env, nil, &depositAddrResp, "account", "deposit-address", "--json", accountID)
	depositAddr := strings.TrimSpace(depositAddrResp.Data.Address)
	if !strings.HasPrefix(depositAddr, "jregtest1") {
		t.Fatalf("unexpected deposit address: %q", depositAddr)
	}

	// Fund the deposit by shielding coinbase directly to the deposit address.
	mustRunCLI(t, ctx, stack, "generate", "101")
	opid := mustShieldCoinbase(t, ctx, stack, depositAddr)
	_ = mustWaitOpTxID(t, ctx, stack, opid)
	mustRunCLI(t, ctx, stack, "generate", "1")

	mustRunKitOK(t, ctx, bin, env, nil, "account", "wait-deposit", accountID, "--timeout", "2m", "--poll", "200ms", "--json")

	var balResp struct {
		Status string `json:"status"`
		Data   struct {
			BalanceZat int64 `json:"balance_zat"`
		} `json:"data"`
	}
	mustRunKitOKInto(t, ctx, bin, env, nil, &balResp, "account", "balance", "--json", accountID)
	if balResp.Data.BalanceZat <= 0 {
		t.Fatalf("expected credited balance, got: %+v", balResp)
	}

	// Sweep hot funds to cold; account balance stays credited, but hot liquidity is gone.
	mustRunKitOK(t, ctx, bin, env, nil, "sweep", "to-cold", "--wait-confirmations", "0", "--json")
	mustRunCLI(t, ctx, stack, "generate", "1")

	sc, err := junoscan.New(stack.ScanURL)
	if err != nil {
		t.Fatalf("junoscan.New: %v", err)
	}
	waitForHotNotes(t, ctx, sc, 0)

	dest := mustCreateOrchardAddress(t, ctx, stack)

	withdrawOut, _ := runKit(t, ctx, bin, env, nil, "withdraw",
		"--account", accountID,
		"--to", dest,
		"--amount-zat", "100000",
		"--wait-confirmations", "0",
		"--json",
	)
	var withdrawErr struct {
		Status string `json:"status"`
		Error  struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.Unmarshal(withdrawOut, &withdrawErr)
	if withdrawErr.Status != "err" || withdrawErr.Error.Code != "no_liquidity_in_hot" || withdrawErr.Error.Message != "NO_LIQUIDITY_IN_HOT" {
		t.Fatalf("expected NO_LIQUIDITY_IN_HOT, got: %s", string(withdrawOut))
	}

	// Move liquidity back to hot using the offline-style flow.
	var planResp struct {
		Status string `json:"status"`
		Data   struct {
			TxPlan any `json:"txplan"`
		} `json:"data"`
		Error any `json:"error"`
	}
	mustRunKitOKInto(t, ctx, bin, env, nil, &planResp, "cold-to-hot", "plan", "--amount-zat", "200000", "--json")
	planBytes, _ := json.Marshal(planResp.Data.TxPlan)

	var signResp struct {
		Status string `json:"status"`
		Data   struct {
			TxID     string `json:"txid"`
			RawTxHex string `json:"raw_tx_hex"`
		} `json:"data"`
		Error any `json:"error"`
	}
	mustRunKitOKInto(t, ctx, bin, env, planBytes, &signResp, "cold-to-hot", "sign", "--txplan", "-", "--json")
	if strings.TrimSpace(signResp.Data.RawTxHex) == "" {
		t.Fatalf("missing raw tx hex from sign")
	}
	mustRunKitOK(t, ctx, bin, env, nil, "cold-to-hot", "broadcast", "--raw-tx-hex", strings.TrimSpace(signResp.Data.RawTxHex), "--wait-confirmations", "0", "--json")
	mustRunCLI(t, ctx, stack, "generate", "1")
	waitForHotNotesAtLeast(t, ctx, sc, 1)

	// Withdraw should succeed now.
	var withdrawOK struct {
		Status string `json:"status"`
		Data   struct {
			TxID   string `json:"txid"`
			FeeZat int64  `json:"fee_zat"`
			Wallet string `json:"wallet_id"`
		} `json:"data"`
		Error any `json:"error"`
	}
	mustRunKitOKInto(t, ctx, bin, env, nil, &withdrawOK, "withdraw",
		"--account", accountID,
		"--to", dest,
		"--amount-zat", "100000",
		"--wait-confirmations", "0",
		"--json",
	)
	if strings.TrimSpace(withdrawOK.Data.TxID) == "" || withdrawOK.Data.FeeZat <= 0 || withdrawOK.Data.Wallet != "hot" {
		t.Fatalf("unexpected withdraw response: %+v", withdrawOK)
	}
	mustRunCLI(t, ctx, stack, "generate", "1")

	// Withdrawals list includes the tx.
	var listResp struct {
		Status string `json:"status"`
		Data   struct {
			Withdrawals []struct {
				TxID *string `json:"txid"`
			} `json:"withdrawals"`
		} `json:"data"`
		Error any `json:"error"`
	}
	mustRunKitOKInto(t, ctx, bin, env, nil, &listResp, "withdrawals", "list", "--account", accountID, "--json")

	found := false
	for _, w := range listResp.Data.Withdrawals {
		if w.TxID != nil && strings.EqualFold(*w.TxID, withdrawOK.Data.TxID) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("withdrawal history missing txid %s: %+v", withdrawOK.Data.TxID, listResp.Data.Withdrawals)
	}

	// Insufficient balance error.
	tooMuch, _ := runKit(t, ctx, bin, env, nil, "withdraw",
		"--account", accountID,
		"--to", dest,
		"--amount-zat", "999999999999999",
		"--wait-confirmations", "0",
		"--json",
	)
	var insuff struct {
		Status string `json:"status"`
		Error  struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.Unmarshal(tooMuch, &insuff)
	if insuff.Status != "err" || insuff.Error.Code != "insufficient_balance" || insuff.Error.Message != "INSUFFICIENT_BALANCE" {
		t.Fatalf("expected INSUFFICIENT_BALANCE, got: %s", string(tooMuch))
	}
}

func waitForHotNotes(t *testing.T, ctx context.Context, sc *junoscan.Client, want int) {
	t.Helper()

	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		notes, err := sc.ListWalletNotes(ctx, "hot", true)
		if err == nil && len(notes) == want {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for hot notes=%d", want)
}

func waitForHotNotesAtLeast(t *testing.T, ctx context.Context, sc *junoscan.Client, min int) {
	t.Helper()

	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		notes, err := sc.ListWalletNotes(ctx, "hot", true)
		if err == nil && len(notes) >= min {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for hot notes >= %d", min)
}

func mustRunCLI(t *testing.T, ctx context.Context, stack *testutil.Stack, args ...string) []byte {
	t.Helper()

	out, _, err := stack.Junocashd.CLI(ctx, args...)
	if err != nil {
		t.Fatalf("junocash-cli %s: %v", strings.Join(args, " "), err)
	}
	return out
}

func mustShieldCoinbase(t *testing.T, ctx context.Context, stack *testutil.Stack, toAddr string) string {
	t.Helper()

	var resp struct {
		OpID string `json:"opid"`
	}
	out := mustRunCLI(t, ctx, stack, "z_shieldcoinbase", "*", toAddr)
	if err := json.Unmarshal(out, &resp); err != nil || strings.TrimSpace(resp.OpID) == "" {
		t.Fatalf("invalid shield response: %v\n%s", err, string(out))
	}
	return strings.TrimSpace(resp.OpID)
}

func mustWaitOpTxID(t *testing.T, ctx context.Context, stack *testutil.Stack, opid string) string {
	t.Helper()

	deadline := time.Now().Add(3 * time.Minute)
	if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
		deadline = dl
	}

	for time.Now().Before(deadline) {
		out := mustRunCLI(t, ctx, stack, "z_getoperationresult", `["`+opid+`"]`)
		var res []struct {
			Status string `json:"status"`
			Result *struct {
				TxID string `json:"txid"`
			} `json:"result,omitempty"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error,omitempty"`
		}
		if err := json.Unmarshal(out, &res); err == nil && len(res) > 0 {
			switch res[0].Status {
			case "success":
				if res[0].Result == nil || strings.TrimSpace(res[0].Result.TxID) == "" {
					t.Fatalf("missing txid in op result: %s", string(out))
				}
				return strings.ToLower(strings.TrimSpace(res[0].Result.TxID))
			case "failed":
				msg := ""
				if res[0].Error != nil {
					msg = res[0].Error.Message
				}
				t.Fatalf("operation failed: %s (%s)", opid, msg)
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("operation did not succeed: %s", opid)
	return ""
}

func mustCreateOrchardAddress(t *testing.T, ctx context.Context, stack *testutil.Stack) string {
	t.Helper()

	var acc struct {
		Account int `json:"account"`
	}
	out := mustRunCLI(t, ctx, stack, "z_getnewaccount")
	if err := json.Unmarshal(out, &acc); err != nil || acc.Account < 0 {
		t.Fatalf("z_getnewaccount: %v\n%s", err, string(out))
	}

	var addrResp struct {
		Address string `json:"address"`
	}
	out = mustRunCLI(t, ctx, stack, "z_getaddressforaccount", itoa(int64(acc.Account)))
	if err := json.Unmarshal(out, &addrResp); err != nil || strings.TrimSpace(addrResp.Address) == "" {
		t.Fatalf("z_getaddressforaccount: %v\n%s", err, string(out))
	}
	return strings.TrimSpace(addrResp.Address)
}

func itoa(n int64) string {
	return strconv.FormatInt(n, 10)
}

func mustRunKitOK(t *testing.T, ctx context.Context, bin string, env map[string]string, stdin []byte, args ...string) {
	t.Helper()
	out, err := runKit(t, ctx, bin, env, stdin, args...)
	if err != nil {
		t.Fatalf("run %s %s: %v\n%s", bin, strings.Join(args, " "), err, string(out))
	}
	var st struct {
		Status string `json:"status"`
	}
	_ = json.Unmarshal(out, &st)
	if st.Status != "ok" {
		t.Fatalf("unexpected response: %s", string(out))
	}
}

func mustRunKitOKInto(t *testing.T, ctx context.Context, bin string, env map[string]string, stdin []byte, into any, args ...string) {
	t.Helper()
	out, err := runKit(t, ctx, bin, env, stdin, args...)
	if err != nil {
		t.Fatalf("run %s %s: %v\n%s", bin, strings.Join(args, " "), err, string(out))
	}
	if err := json.Unmarshal(out, into); err != nil {
		t.Fatalf("invalid json: %v\n%s", err, string(out))
	}
}

func runKit(t *testing.T, ctx context.Context, bin string, env map[string]string, stdin []byte, args ...string) ([]byte, error) {
	t.Helper()

	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = append([]string{}, os.Environ()...)
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		// CLI JSON errors are written to stdout; keep combined context for debugging.
		if s := strings.TrimSpace(stderr.String()); s != "" {
			return append(stdout.Bytes(), []byte("\n\nstderr:\n"+s)...), err
		}
		return stdout.Bytes(), err
	}
	return stdout.Bytes(), nil
}
