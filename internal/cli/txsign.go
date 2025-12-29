package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/Abdullah1738/juno-sdk-go/types"
)

type txsignResult struct {
	TxID     string
	RawTxHex string
	FeeZat   string
}

func txsignBinFromEnv() string {
	if v := strings.TrimSpace(os.Getenv("JUNO_TXSIGN_BIN")); v != "" {
		return v
	}
	return "juno-txsign"
}

func signTxPlan(ctx context.Context, txsignBin string, seedPath string, plan types.TxPlan) (txsignResult, error) {
	txsignBin = strings.TrimSpace(txsignBin)
	seedPath = strings.TrimSpace(seedPath)
	if txsignBin == "" {
		return txsignResult{}, errors.New("txsign bin required")
	}
	if seedPath == "" {
		return txsignResult{}, errors.New("seed path required")
	}

	body, err := json.Marshal(plan)
	if err != nil {
		return txsignResult{}, errors.New("invalid txplan")
	}

	if ctx == nil {
		var cancel func()
		ctx, cancel = context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
	}

	cmd := exec.CommandContext(ctx, txsignBin,
		"sign",
		"--txplan", "-",
		"--seed-file", seedPath,
		"--json",
	)
	cmd.Stdin = bytes.NewReader(body)

	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return txsignResult{}, fmt.Errorf("txsign failed: %s", msg)
	}

	var resp struct {
		Status string `json:"status"`
		Data   struct {
			TxID     string `json:"txid"`
			RawTxHex string `json:"raw_tx_hex"`
			FeeZat   string `json:"fee_zat"`
		} `json:"data"`
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		return txsignResult{}, errors.New("txsign: invalid json response")
	}
	if strings.TrimSpace(resp.Status) != "ok" {
		msg := strings.TrimSpace(resp.Error.Message)
		if msg == "" {
			msg = strings.TrimSpace(resp.Error.Code)
		}
		if msg == "" {
			msg = "sign_failed"
		}
		return txsignResult{}, fmt.Errorf("txsign failed: %s", msg)
	}

	txid := strings.ToLower(strings.TrimSpace(resp.Data.TxID))
	raw := strings.TrimSpace(resp.Data.RawTxHex)
	fee := strings.TrimSpace(resp.Data.FeeZat)
	if txid == "" || raw == "" || fee == "" {
		return txsignResult{}, errors.New("txsign: invalid response")
	}

	return txsignResult{TxID: txid, RawTxHex: raw, FeeZat: fee}, nil
}
