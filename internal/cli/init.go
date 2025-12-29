package cli

import (
	"context"
	"flag"
	"io"
	"time"
)

func runInit(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
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

	if err := s.Init(ctx); err != nil {
		return writeErr(stdout, stderr, common.jsonOut, "io_error", err.Error())
	}

	return writeOK(stdout, common.jsonOut, map[string]any{"data_dir": dataDir})
}

