//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Abdullah1738/juno-exchange-kit/internal/cli"
	"github.com/Abdullah1738/juno-exchange-kit/internal/testutil"
	"github.com/Abdullah1738/juno-sdk-go/junoscan"
)

func TestExchangeKit_InitAndRegisterWallets_Integration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	dataDir := t.TempDir()

	stack, err := testutil.StartStack(ctx, filepath.Join(dataDir, "stack"), testutil.StartStackConfig{
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

	withEnv(t, map[string]string{
		"JUNO_EXCHANGE_KIT_DATA_DIR": filepath.Join(dataDir, "kit"),
		"JUNO_RPC_URL":               stack.Junocashd.RPCURL,
		"JUNO_RPC_USER":              stack.Junocashd.RPCUser,
		"JUNO_RPC_PASS":              stack.Junocashd.RPCPassword,
		"JUNO_SCAN_URL":              stack.ScanURL,
		"JUNO_BROADCAST_URL":         stack.BroadcastURL,
		"JUNO_TXSIGN_BIN":            txsignBin,
	}, func() {
		var stdout, stderr bytes.Buffer
		code := cli.RunWithIO([]string{"init", "--json"}, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("init failed: code=%d\nstderr=%s\nstdout=%s", code, stderr.String(), stdout.String())
		}

		var initResp struct {
			Status string `json:"status"`
			Data   struct {
				UAHRP string `json:"ua_hrp"`
			} `json:"data"`
			Error any `json:"error"`
		}
		if err := json.Unmarshal(stdout.Bytes(), &initResp); err != nil {
			t.Fatalf("init invalid json: %v\n%s", err, stdout.String())
		}
		if initResp.Status != "ok" || initResp.Data.UAHRP != "jregtest" {
			t.Fatalf("unexpected init response: %s", stdout.String())
		}

		sc, err := junoscan.New(stack.ScanURL)
		if err != nil {
			t.Fatalf("junoscan.New: %v", err)
		}
		wallets, err := sc.ListWallets(ctx)
		if err != nil {
			t.Fatalf("ListWallets: %v", err)
		}

		var haveHot, haveCold bool
		for _, w := range wallets {
			if w.WalletID == "hot" {
				haveHot = true
			}
			if w.WalletID == "cold" {
				haveCold = true
			}
		}
		if !haveHot || !haveCold {
			t.Fatalf("wallets not registered (hot=%v cold=%v): %+v", haveHot, haveCold, wallets)
		}

		stdout.Reset()
		stderr.Reset()
		code = cli.RunWithIO([]string{"account", "create"}, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("account create failed: code=%d\nstderr=%s\nstdout=%s", code, stderr.String(), stdout.String())
		}
		accountID := strings.TrimSpace(stdout.String())
		if accountID == "" {
			t.Fatalf("missing account_id")
		}

		stdout.Reset()
		stderr.Reset()
		code = cli.RunWithIO([]string{"account", "deposit-address", accountID, "--json"}, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("account deposit-address failed: code=%d\nstderr=%s\nstdout=%s", code, stderr.String(), stdout.String())
		}
		var depResp struct {
			Status string `json:"status"`
			Data   struct {
				Address string `json:"address"`
			} `json:"data"`
		}
		if err := json.Unmarshal(stdout.Bytes(), &depResp); err != nil || depResp.Status != "ok" || strings.TrimSpace(depResp.Data.Address) == "" {
			t.Fatalf("deposit-address invalid json: %v\n%s", err, stdout.String())
		}

		stdout.Reset()
		stderr.Reset()
		code = cli.RunWithIO([]string{"account", "list", "--json"}, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("account list failed: code=%d\nstderr=%s\nstdout=%s", code, stderr.String(), stdout.String())
		}
		var listResp struct {
			Status string `json:"status"`
			Data   struct {
				Accounts []struct {
					AccountID  string `json:"account_id"`
					BalanceZat int64  `json:"balance_zat"`
				} `json:"accounts"`
			} `json:"data"`
		}
		if err := json.Unmarshal(stdout.Bytes(), &listResp); err != nil || listResp.Status != "ok" {
			t.Fatalf("account list invalid json: %v\n%s", err, stdout.String())
		}
		found := false
		for _, a := range listResp.Data.Accounts {
			if a.AccountID == accountID && a.BalanceZat == 0 {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("account list missing %s: %+v", accountID, listResp.Data.Accounts)
		}

		stdout.Reset()
		stderr.Reset()
		code = cli.RunWithIO([]string{"account", "balance", accountID}, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("account balance failed: code=%d\nstderr=%s\nstdout=%s", code, stderr.String(), stdout.String())
		}
		if strings.TrimSpace(stdout.String()) != "0" {
			t.Fatalf("unexpected initial balance: %q", stdout.String())
		}

		stdout.Reset()
		stderr.Reset()
		code = cli.RunWithIO([]string{"wallet", "balance", "hot", "--minconf", "0", "--json"}, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("wallet balance failed: code=%d\nstderr=%s\nstdout=%s", code, stderr.String(), stdout.String())
		}

		stdout.Reset()
		stderr.Reset()
		code = cli.RunWithIO([]string{"wallet", "addresses", "hot", "--scope", "external", "--json"}, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("wallet addresses failed: code=%d\nstderr=%s\nstdout=%s", code, stderr.String(), stdout.String())
		}
		var addrResp struct {
			Status string `json:"status"`
			Data   struct {
				Addresses []struct {
					AccountID string `json:"account_id"`
					Address   string `json:"address"`
				} `json:"addresses"`
			} `json:"data"`
		}
		if err := json.Unmarshal(stdout.Bytes(), &addrResp); err != nil || addrResp.Status != "ok" {
			t.Fatalf("wallet addresses invalid json: %v\n%s", err, stdout.String())
		}
		found = false
		for _, a := range addrResp.Data.Addresses {
			if a.AccountID == accountID && strings.TrimSpace(a.Address) == strings.TrimSpace(depResp.Data.Address) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("wallet addresses missing %s: %+v", depResp.Data.Address, addrResp.Data.Addresses)
		}

		stdout.Reset()
		stderr.Reset()
		code = cli.RunWithIO([]string{"balances", "--minconf", "0", "--json"}, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("balances failed: code=%d\nstderr=%s\nstdout=%s", code, stderr.String(), stdout.String())
		}
		var balAll struct {
			Status string `json:"status"`
			Data   struct {
				AssetsZat      int64 `json:"assets_zat"`
				LiabilitiesZat int64 `json:"liabilities_zat"`
			} `json:"data"`
		}
		if err := json.Unmarshal(stdout.Bytes(), &balAll); err != nil || balAll.Status != "ok" {
			t.Fatalf("balances invalid json: %v\n%s", err, stdout.String())
		}
		if balAll.Data.AssetsZat != 0 || balAll.Data.LiabilitiesZat != 0 {
			t.Fatalf("unexpected balances: %+v", balAll.Data)
		}
	})
}

func withEnv(t *testing.T, kv map[string]string, fn func()) {
	t.Helper()

	orig := make(map[string]*string, len(kv))
	for k := range kv {
		if v, ok := os.LookupEnv(k); ok {
			vv := v
			orig[k] = &vv
		} else {
			orig[k] = nil
		}
	}

	for k, v := range kv {
		if err := os.Setenv(k, v); err != nil {
			t.Fatalf("setenv %s: %v", k, err)
		}
	}
	defer func() {
		for k, old := range orig {
			if old == nil {
				_ = os.Unsetenv(k)
				continue
			}
			_ = os.Setenv(k, *old)
		}
	}()

	fn()
}
