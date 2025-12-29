//go:build integration || e2e

package testutil

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Abdullah1738/juno-exchange-kit/internal/testutil/containers"
)

type Stack struct {
	Junocashd *containers.Junocashd

	ScanURL      string
	BroadcastURL string

	scanProc      *Process
	broadcastProc *Process
}

type StartStackConfig struct {
	UaHRP         string
	Confirmations int64
}

func StartStack(ctx context.Context, dataDir string, cfg StartStackConfig) (*Stack, error) {
	if ctx == nil {
		return nil, fmt.Errorf("stack: ctx is nil")
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, err
	}
	if cfg.UaHRP == "" {
		cfg.UaHRP = "jregtest"
	}
	if cfg.Confirmations <= 0 {
		cfg.Confirmations = 1
	}

	jd, err := containers.StartJunocashd(ctx)
	if err != nil {
		return nil, err
	}

	scanPort, err := FreePort()
	if err != nil {
		_ = jd.Terminate(context.Background())
		return nil, err
	}
	broadcastPort, err := FreePort()
	if err != nil {
		_ = jd.Terminate(context.Background())
		return nil, err
	}

	scanURL := fmt.Sprintf("http://127.0.0.1:%d", scanPort)
	broadcastURL := fmt.Sprintf("http://127.0.0.1:%d", broadcastPort)

	scanBin, err := EnsureTool(ctx, ToolSpec{
		EnvVar:      "JUNO_TEST_JUNO_SCAN_BIN",
		BinaryName:  "juno-scan",
		SiblingPath: filepath.Join("..", "juno-scan", "bin", "juno-scan"),
		BuildDir:    filepath.Join("..", "juno-scan"),
	})
	if err != nil {
		_ = jd.Terminate(context.Background())
		return nil, err
	}
	broadcastBin, err := EnsureTool(ctx, ToolSpec{
		EnvVar:      "JUNO_TEST_JUNO_BROADCAST_BIN",
		BinaryName:  "juno-broadcast",
		SiblingPath: filepath.Join("..", "juno-broadcast", "bin", "juno-broadcast"),
		BuildDir:    filepath.Join("..", "juno-broadcast"),
	})
	if err != nil {
		_ = jd.Terminate(context.Background())
		return nil, err
	}

	bcastProc, err := StartProcess(ctx, broadcastBin, []string{
		"serve",
		"--rpc-url", jd.RPCURL,
		"--rpc-user", jd.RPCUser,
		"--rpc-pass", jd.RPCPassword,
		"--listen", fmt.Sprintf("127.0.0.1:%d", broadcastPort),
	}, nil)
	if err != nil {
		_ = jd.Terminate(context.Background())
		return nil, err
	}

	scanDBPath := filepath.Join(dataDir, "juno-scan-db")
	scProc, err := StartProcess(ctx, scanBin, []string{
		"-listen", fmt.Sprintf("127.0.0.1:%d", scanPort),
		"-rpc-url", jd.RPCURL,
		"-rpc-user", jd.RPCUser,
		"-rpc-pass", jd.RPCPassword,
		"-ua-hrp", cfg.UaHRP,
		"-confirmations", fmt.Sprint(cfg.Confirmations),
		"-db-driver", "rocksdb",
		"-db-path", scanDBPath,
	}, nil)
	if err != nil {
		_ = bcastProc.Terminate(context.Background())
		_ = jd.Terminate(context.Background())
		return nil, err
	}

	readyCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	if err := WaitForHTTP200(readyCtx, broadcastURL+"/healthz"); err != nil {
		_ = scProc.Terminate(context.Background())
		_ = bcastProc.Terminate(context.Background())
		_ = jd.Terminate(context.Background())
		return nil, err
	}
	if err := WaitForHTTP200(readyCtx, scanURL+"/v1/health"); err != nil {
		_ = scProc.Terminate(context.Background())
		_ = bcastProc.Terminate(context.Background())
		_ = jd.Terminate(context.Background())
		return nil, err
	}

	return &Stack{
		Junocashd:     jd,
		ScanURL:       scanURL,
		BroadcastURL:  broadcastURL,
		scanProc:      scProc,
		broadcastProc: bcastProc,
	}, nil
}

func (s *Stack) Close(ctx context.Context) {
	if s == nil {
		return
	}
	_ = s.scanProc.Terminate(context.Background())
	_ = s.broadcastProc.Terminate(context.Background())
	_ = s.Junocashd.Terminate(context.Background())
}
