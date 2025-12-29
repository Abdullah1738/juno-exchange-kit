package cli

import (
	"context"
	"flag"
	"io"
	"time"

	"github.com/Abdullah1738/juno-sdk-go/junoscan"
)

func runDaemon(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("daemon", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	var common commonFlags
	common.bind(fs)

	var services servicesFlags
	services.bind(fs)

	var poll time.Duration
	var once bool
	fs.DurationVar(&poll, "poll", 2*time.Second, "poll interval")
	fs.BoolVar(&once, "once", false, "run a single sync iteration then exit")

	if err := fs.Parse(args); err != nil {
		return 2
	}
	if poll <= 0 {
		return writeErr(stdout, stderr, common.jsonOut, "invalid_request", "poll must be > 0")
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

	scanURL := services.resolvedScanURL()
	if scanURL == "" {
		return writeErr(stdout, stderr, common.jsonOut, "invalid_request", "scan url required (set --scan-url or JUNO_SCAN_URL)")
	}
	sc, err := junoscan.New(scanURL)
	if err != nil {
		return writeErr(stdout, stderr, common.jsonOut, "invalid_request", err.Error())
	}

	wallets := []string{"hot", "cold"}
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		for _, walletID := range wallets {
			if _, err := syncWallet(ctx, st, sc, walletID, stdout, common.jsonOut); err != nil {
				cancel()
				return writeErr(stdout, stderr, common.jsonOut, "sync_failed", err.Error())
			}
		}
		cancel()

		if once {
			break
		}

		time.Sleep(poll)
	}

	return writeOK(stdout, common.jsonOut, map[string]any{"running": false})
}
