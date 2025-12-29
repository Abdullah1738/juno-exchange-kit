//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
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
