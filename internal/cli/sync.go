package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"strings"
	"time"

	"github.com/Abdullah1738/juno-exchange-kit/internal/store"
	"github.com/Abdullah1738/juno-sdk-go/junoscan"
	"github.com/Abdullah1738/juno-sdk-go/types"
)

func runSync(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("sync", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	var common commonFlags
	common.bind(fs)

	var services servicesFlags
	services.bind(fs)

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

	scanURL := services.resolvedScanURL()
	if scanURL == "" {
		return writeErr(stdout, stderr, common.jsonOut, "invalid_request", "scan url required (set --scan-url or JUNO_SCAN_URL)")
	}
	sc, err := junoscan.New(scanURL)
	if err != nil {
		return writeErr(stdout, stderr, common.jsonOut, "invalid_request", err.Error())
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	results := make([]map[string]any, 0, 2)
	wallets := []string{"hot", "cold"}
	for _, walletID := range wallets {
		r, err := syncWallet(ctx, s, sc, walletID, stdout, common.jsonOut)
		if err != nil {
			return writeErr(stdout, stderr, common.jsonOut, "sync_failed", err.Error())
		}
		results = append(results, map[string]any{
			"wallet_id": walletID,
			"cursor":    r.Cursor,
			"events":    r.Events,
			"credits":   r.Credits,
			"debits":    r.Debits,
		})
	}

	return writeOK(stdout, common.jsonOut, map[string]any{"synced": true, "wallets": results})
}

type syncResult struct {
	Cursor  int64
	Events  int64
	Credits int64
	Debits  int64
}

func syncWallet(ctx context.Context, st *store.Store, sc *junoscan.Client, walletID string, stdout io.Writer, jsonOut bool) (syncResult, error) {
	cursor, err := st.GetScanCursor(ctx, walletID)
	if err != nil {
		return syncResult{}, err
	}

	var out syncResult
	for {
		page, err := sc.ListWalletEvents(ctx, walletID, cursor, 200)
		if err != nil {
			return syncResult{}, err
		}
		if len(page.Events) == 0 {
			cursor = page.NextCursor
			break
		}

		for _, ev := range page.Events {
			out.Events++
			switch ev.Kind {
			case types.WalletEventKindDepositEvent:
				var p types.DepositEventPayload
				if err := json.Unmarshal(ev.Payload, &p); err != nil {
					return syncResult{}, errorsNew("invalid deposit payload")
				}
				res, err := st.ApplyDeposit(ctx, walletID, p.TxID, p.ActionIndex, p.DiversifierIndex, p.RecipientAddress, amountI64(p.AmountZatoshis), p.Height, store.DepositStatusDetected, nil)
				if err != nil {
					return syncResult{}, err
				}
				if !jsonOut && strings.TrimSpace(res.AccountID) != "" {
					fmt.Fprintf(stdout, "pending %s %s JUNO (txid=%s)\n", res.AccountID, formatJUNO(amountI64(p.AmountZatoshis)), strings.ToLower(strings.TrimSpace(p.TxID)))
				}
			case types.WalletEventKindDepositConfirmed:
				var p types.DepositConfirmedPayload
				if err := json.Unmarshal(ev.Payload, &p); err != nil {
					return syncResult{}, errorsNew("invalid deposit confirmed payload")
				}
				ch := p.ConfirmedHeight
				res, err := st.ApplyDeposit(ctx, walletID, p.TxID, p.ActionIndex, p.DiversifierIndex, p.RecipientAddress, amountI64(p.AmountZatoshis), p.Height, store.DepositStatusConfirmed, &ch)
				if err != nil {
					return syncResult{}, err
				}
				if res.DeltaZat > 0 {
					out.Credits += res.DeltaZat
					if !jsonOut && strings.TrimSpace(res.AccountID) != "" {
						fmt.Fprintf(stdout, "credited %s %d (txid=%s)\n", res.AccountID, res.DeltaZat, strings.ToLower(strings.TrimSpace(p.TxID)))
					}
				}
			case types.WalletEventKindDepositUnconfirmed:
				var p types.DepositUnconfirmedPayload
				if err := json.Unmarshal(ev.Payload, &p); err != nil {
					return syncResult{}, errorsNew("invalid deposit unconfirmed payload")
				}
				res, err := st.ApplyDeposit(ctx, walletID, p.TxID, p.ActionIndex, p.DiversifierIndex, p.RecipientAddress, amountI64(p.AmountZatoshis), p.Height, store.DepositStatusUnconfirmed, nil)
				if err != nil {
					return syncResult{}, err
				}
				if res.DeltaZat < 0 {
					out.Debits += -res.DeltaZat
				}
			case types.WalletEventKindDepositOrphaned:
				var p types.DepositOrphanedPayload
				if err := json.Unmarshal(ev.Payload, &p); err != nil {
					return syncResult{}, errorsNew("invalid deposit orphaned payload")
				}
				res, err := st.ApplyDeposit(ctx, walletID, p.TxID, p.ActionIndex, p.DiversifierIndex, p.RecipientAddress, amountI64(p.AmountZatoshis), p.Height, store.DepositStatusOrphaned, nil)
				if err != nil {
					return syncResult{}, err
				}
				if res.DeltaZat < 0 {
					out.Debits += -res.DeltaZat
				}
			default:
				// Ignore spend events for now (handled by withdrawal tracking).
			}
		}

		cursor = page.NextCursor
	}

	if err := st.SetScanCursor(ctx, walletID, cursor); err != nil {
		return syncResult{}, err
	}

	out.Cursor = cursor
	return out, nil
}

func amountI64(v uint64) int64 {
	if v > uint64(math.MaxInt64) {
		return math.MaxInt64
	}
	return int64(v)
}

func errorsNew(msg string) error { return fmt.Errorf("sync: %s", msg) }
