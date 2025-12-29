package cli

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Abdullah1738/juno-exchange-kit/internal/keys"
	keysffi "github.com/Abdullah1738/juno-exchange-kit/internal/keys/ffi"
	"github.com/Abdullah1738/juno-sdk-go/junocashd"
	"github.com/Abdullah1738/juno-sdk-go/junoscan"
)

func runInit(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	var common commonFlags
	common.bind(fs)

	var rpc rpcFlags
	rpc.bind(fs)

	var services servicesFlags
	services.bind(fs)

	var uaHRPOverride string
	fs.StringVar(&uaHRPOverride, "ua-hrp", "", "unified address HRP override (e.g., jregtest)")

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

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if err := s.Init(ctx); err != nil {
		return writeErr(stdout, stderr, common.jsonOut, "io_error", err.Error())
	}

	if v, ok, err := s.Meta(ctx, "initialized"); err == nil && ok && strings.TrimSpace(v) == "1" {
		return writeOK(stdout, common.jsonOut, map[string]any{"data_dir": dataDir, "initialized": true})
	}

	rpcURL, rpcUser, rpcPass, err := rpc.resolved()
	if err != nil {
		return writeErr(stdout, stderr, common.jsonOut, "invalid_request", err.Error())
	}

	rpcClient := junocashd.New(rpcURL, rpcUser, rpcPass)
	info, err := rpcClient.GetBlockchainInfo(ctx)
	if err != nil {
		return writeErr(stdout, stderr, common.jsonOut, "node_rpc_error", err.Error())
	}

	uaHRP := strings.TrimSpace(uaHRPOverride)
	if uaHRP == "" {
		var ok bool
		uaHRP, ok = uaHRPFromChain(info.Chain)
		if !ok {
			return writeErr(stdout, stderr, common.jsonOut, "invalid_request", "unknown chain; set --ua-hrp")
		}
	}

	coinType, ok := coinTypeFromChain(info.Chain)
	if !ok {
		return writeErr(stdout, stderr, common.jsonOut, "invalid_request", "unknown chain; set --ua-hrp and configure coin type")
	}

	seedDir := filepath.Join(dataDir, "keys")
	if err := os.MkdirAll(seedDir, 0o755); err != nil {
		return writeErr(stdout, stderr, common.jsonOut, "io_error", err.Error())
	}

	hotSeedPath := filepath.Join(seedDir, "hot.seed")
	coldSeedPath := filepath.Join(seedDir, "cold.seed")

	hotSeedB64, err := ensureSeedFile(hotSeedPath)
	if err != nil {
		return writeErr(stdout, stderr, common.jsonOut, "io_error", err.Error())
	}
	coldSeedB64, err := ensureSeedFile(coldSeedPath)
	if err != nil {
		return writeErr(stdout, stderr, common.jsonOut, "io_error", err.Error())
	}

	deriver := keysffi.New()
	hotUFVK, err := deriver.UFVKFromSeedBase64(hotSeedB64, uaHRP, coinType, 0)
	if err != nil {
		if errors.Is(err, keys.ErrUnavailable) {
			return writeErr(stdout, stderr, common.jsonOut, "keys_unavailable", "key derivation unavailable (build with CGO and run `make rust-build`)")
		}
		return writeErr(stdout, stderr, common.jsonOut, "keys_error", err.Error())
	}
	coldUFVK, err := deriver.UFVKFromSeedBase64(coldSeedB64, uaHRP, coinType, 0)
	if err != nil {
		return writeErr(stdout, stderr, common.jsonOut, "keys_error", err.Error())
	}

	if err := s.UpsertWallet(ctx, "hot", hotUFVK, hotSeedPath); err != nil {
		return writeErr(stdout, stderr, common.jsonOut, "db_error", err.Error())
	}
	if err := s.UpsertWallet(ctx, "cold", coldUFVK, coldSeedPath); err != nil {
		return writeErr(stdout, stderr, common.jsonOut, "db_error", err.Error())
	}
	if err := s.EnsureScanCursor(ctx, "hot"); err != nil {
		return writeErr(stdout, stderr, common.jsonOut, "db_error", err.Error())
	}
	if err := s.EnsureScanCursor(ctx, "cold"); err != nil {
		return writeErr(stdout, stderr, common.jsonOut, "db_error", err.Error())
	}

	_ = s.SetMeta(ctx, "ua_hrp", uaHRP)
	_ = s.SetMeta(ctx, "coin_type", fmt.Sprint(coinType))
	_ = s.SetMeta(ctx, "account", "0")
	_ = s.SetMeta(ctx, "initialized", "1")

	if scanURL := services.resolvedScanURL(); scanURL != "" {
		sc, err := junoscan.New(scanURL)
		if err != nil {
			return writeErr(stdout, stderr, common.jsonOut, "invalid_request", err.Error())
		}
		_ = sc.UpsertWallet(ctx, "hot", hotUFVK)
		_ = sc.UpsertWallet(ctx, "cold", coldUFVK)
	}

	return writeOK(stdout, common.jsonOut, map[string]any{
		"data_dir":  dataDir,
		"ua_hrp":    uaHRP,
		"coin_type": coinType,
		"wallets": []map[string]any{
			{"wallet_id": "hot", "ufvk_hrp": strings.SplitN(hotUFVK, "1", 2)[0]},
			{"wallet_id": "cold", "ufvk_hrp": strings.SplitN(coldUFVK, "1", 2)[0]},
		},
	})
}

func ensureSeedFile(path string) (string, error) {
	if b, err := os.ReadFile(path); err == nil {
		v := strings.TrimSpace(string(b))
		if v != "" {
			return v, nil
		}
	}

	seed := make([]byte, 64)
	if _, err := rand.Read(seed); err != nil {
		return "", err
	}
	seedB64 := base64.StdEncoding.EncodeToString(seed)
	if err := os.WriteFile(path, []byte(seedB64+"\n"), 0o600); err != nil {
		return "", err
	}
	return seedB64, nil
}

func uaHRPFromChain(chain string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(chain)) {
	case "main":
		return "j", true
	case "test":
		return "jtest", true
	case "regtest":
		return "jregtest", true
	default:
		return "", false
	}
}

func coinTypeFromChain(chain string) (uint32, bool) {
	switch strings.ToLower(strings.TrimSpace(chain)) {
	case "main":
		return 8133, true
	case "test":
		return 8134, true
	case "regtest":
		return 8135, true
	default:
		return 0, false
	}
}
