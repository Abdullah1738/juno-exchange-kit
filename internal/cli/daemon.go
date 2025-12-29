package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Abdullah1738/juno-sdk-go/junoscan"
)

func runDaemon(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		switch args[0] {
		case "start":
			return runDaemonStart(args[1:], stdout, stderr)
		case "stop":
			return runDaemonStop(args[1:], stdout, stderr)
		case "status":
			return runDaemonStatus(args[1:], stdout, stderr)
		case "run":
			return runDaemonRun(args[1:], stdout, stderr)
		}
	}
	return runDaemonRun(args, stdout, stderr)
}

const (
	daemonPIDFileName = "daemon.pid"
	daemonLogFileName = "daemon.log"
)

func runDaemonStart(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("daemon start", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	var common commonFlags
	common.bind(fs)

	var services servicesFlags
	services.bind(fs)

	var poll time.Duration
	fs.DurationVar(&poll, "poll", 2*time.Second, "poll interval")
	var logFile string
	fs.StringVar(&logFile, "log-file", "", "daemon log file (default: <data-dir>/daemon.log)")

	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		return writeErr(stdout, stderr, common.jsonOut, "invalid_request", "daemon start takes no positional args")
	}
	if poll <= 0 {
		return writeErr(stdout, stderr, common.jsonOut, "invalid_request", "poll must be > 0")
	}

	dataDir, err := common.resolvedDataDir()
	if err != nil {
		return writeErr(stdout, stderr, common.jsonOut, "invalid_request", err.Error())
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return writeErr(stdout, stderr, common.jsonOut, "io_error", fmt.Sprintf("mkdir data dir: %v", err))
	}

	scanURL := services.resolvedScanURL()
	if scanURL == "" {
		return writeErr(stdout, stderr, common.jsonOut, "invalid_request", "scan url required (set --scan-url or JUNO_SCAN_URL)")
	}
	if _, err := junoscan.New(scanURL); err != nil {
		return writeErr(stdout, stderr, common.jsonOut, "invalid_request", err.Error())
	}

	pidFile := filepath.Join(dataDir, daemonPIDFileName)
	logPath := strings.TrimSpace(logFile)
	if logPath == "" {
		logPath = filepath.Join(dataDir, daemonLogFileName)
	}

	if st, ok, err := daemonStatus(pidFile); err != nil {
		return writeErr(stdout, stderr, common.jsonOut, "io_error", err.Error())
	} else if ok && st.Running {
		return writeErr(stdout, stderr, common.jsonOut, "already_running", fmt.Sprintf("daemon already running (pid=%d)", st.Pid))
	}

	_ = os.Remove(pidFile)

	lf, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return writeErr(stdout, stderr, common.jsonOut, "io_error", fmt.Sprintf("open log file: %v", err))
	}
	defer lf.Close()

	exe, err := os.Executable()
	if err != nil || strings.TrimSpace(exe) == "" {
		return writeErr(stdout, stderr, common.jsonOut, "io_error", "resolve executable path failed")
	}

	childArgs := []string{"daemon", "--poll", poll.String(), "--data-dir", dataDir}
	if strings.TrimSpace(services.scanURL) != "" {
		childArgs = append(childArgs, "--scan-url", strings.TrimSpace(services.scanURL))
	}
	cmd := exec.Command(exe, childArgs...)
	cmd.Env = append([]string{}, os.Environ()...)
	cmd.Stdout = lf
	cmd.Stderr = lf
	if runtime.GOOS != "windows" {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	}

	if err := cmd.Start(); err != nil {
		return writeErr(stdout, stderr, common.jsonOut, "io_error", fmt.Sprintf("start daemon: %v", err))
	}
	if cmd.Process == nil || cmd.Process.Pid <= 0 {
		return writeErr(stdout, stderr, common.jsonOut, "io_error", "start daemon: missing pid")
	}

	pid := cmd.Process.Pid
	if err := writePIDFile(pidFile, pid); err != nil {
		_ = cmd.Process.Kill()
		return writeErr(stdout, stderr, common.jsonOut, "io_error", err.Error())
	}

	if err := cmd.Process.Release(); err != nil {
		// Best-effort; the child is already running.
	}

	return writeOK(stdout, common.jsonOut, map[string]any{
		"running":  true,
		"pid":      pid,
		"pid_file": pidFile,
		"log_file": logPath,
	})
}

func runDaemonStatus(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("daemon status", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	var common commonFlags
	common.bind(fs)

	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		return writeErr(stdout, stderr, common.jsonOut, "invalid_request", "daemon status takes no positional args")
	}

	dataDir, err := common.resolvedDataDir()
	if err != nil {
		return writeErr(stdout, stderr, common.jsonOut, "invalid_request", err.Error())
	}

	pidFile := filepath.Join(dataDir, daemonPIDFileName)
	st, ok, err := daemonStatus(pidFile)
	if err != nil {
		return writeErr(stdout, stderr, common.jsonOut, "io_error", err.Error())
	}
	if !ok {
		return writeOK(stdout, common.jsonOut, map[string]any{"running": false})
	}
	if !st.Running {
		_ = os.Remove(pidFile)
	}
	return writeOK(stdout, common.jsonOut, map[string]any{
		"running":  st.Running,
		"pid":      st.Pid,
		"pid_file": pidFile,
	})
}

func runDaemonStop(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("daemon stop", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	var common commonFlags
	common.bind(fs)

	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		return writeErr(stdout, stderr, common.jsonOut, "invalid_request", "daemon stop takes no positional args")
	}

	dataDir, err := common.resolvedDataDir()
	if err != nil {
		return writeErr(stdout, stderr, common.jsonOut, "invalid_request", err.Error())
	}

	pidFile := filepath.Join(dataDir, daemonPIDFileName)
	pid, ok, err := readPIDFile(pidFile)
	if err != nil {
		return writeErr(stdout, stderr, common.jsonOut, "io_error", err.Error())
	}
	if !ok {
		return writeOK(stdout, common.jsonOut, map[string]any{"stopped": true, "running": false})
	}

	running, err := isProcessRunning(pid)
	if err != nil {
		return writeErr(stdout, stderr, common.jsonOut, "io_error", err.Error())
	}
	if !running {
		_ = os.Remove(pidFile)
		return writeOK(stdout, common.jsonOut, map[string]any{"stopped": true, "running": false, "pid": pid})
	}

	if err := terminateProcess(pid, 5*time.Second); err != nil {
		return writeErr(stdout, stderr, common.jsonOut, "io_error", err.Error())
	}
	_ = os.Remove(pidFile)

	return writeOK(stdout, common.jsonOut, map[string]any{"stopped": true, "running": false, "pid": pid})
}

func runDaemonRun(args []string, stdout, stderr io.Writer) int {
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
	if fs.NArg() != 0 {
		return writeErr(stdout, stderr, common.jsonOut, "invalid_request", "daemon takes no positional args")
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

	pidFile := filepath.Join(dataDir, daemonPIDFileName)
	defer func() { _ = removePIDFileIfMatches(pidFile, os.Getpid()) }()

	scanURL := services.resolvedScanURL()
	if scanURL == "" {
		return writeErr(stdout, stderr, common.jsonOut, "invalid_request", "scan url required (set --scan-url or JUNO_SCAN_URL)")
	}
	sc, err := junoscan.New(scanURL)
	if err != nil {
		return writeErr(stdout, stderr, common.jsonOut, "invalid_request", err.Error())
	}

	wallets := []string{"hot", "cold"}

	stopCh := make(chan os.Signal, 2)
	signal.Notify(stopCh, os.Interrupt)
	if runtime.GOOS != "windows" {
		signal.Notify(stopCh, syscall.SIGTERM)
	}
	defer signal.Stop(stopCh)

loop:
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

		select {
		case <-stopCh:
			break loop
		case <-time.After(poll):
		}
	}

	return writeOK(stdout, common.jsonOut, map[string]any{"running": false})
}

type daemonStatusResult struct {
	Running bool
	Pid     int
}

func daemonStatus(pidFile string) (daemonStatusResult, bool, error) {
	pid, ok, err := readPIDFile(pidFile)
	if err != nil {
		return daemonStatusResult{}, false, err
	}
	if !ok {
		return daemonStatusResult{}, false, nil
	}
	running, err := isProcessRunning(pid)
	if err != nil {
		return daemonStatusResult{}, true, err
	}
	return daemonStatusResult{Running: running, Pid: pid}, true, nil
}

func readPIDFile(path string) (int, bool, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, false, nil
		}
		return 0, false, fmt.Errorf("read pid file: %w", err)
	}
	s := strings.TrimSpace(string(raw))
	if s == "" {
		return 0, false, fmt.Errorf("read pid file: empty")
	}
	pid64, err := strconv.ParseInt(s, 10, 0)
	if err != nil || pid64 <= 0 || pid64 > int64(^uint(0)>>1) {
		return 0, false, fmt.Errorf("read pid file: invalid pid %q", s)
	}
	return int(pid64), true, nil
}

func writePIDFile(path string, pid int) error {
	if pid <= 0 {
		return fmt.Errorf("write pid file: invalid pid %d", pid)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(strconv.Itoa(pid)+"\n"), 0o644); err != nil {
		return fmt.Errorf("write pid file: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("write pid file: %w", err)
	}
	return nil
}

func removePIDFileIfMatches(path string, pid int) error {
	p, ok, err := readPIDFile(path)
	if err != nil || !ok {
		return err
	}
	if p != pid {
		return nil
	}
	return os.Remove(path)
}

func isProcessRunning(pid int) (bool, error) {
	if pid <= 0 {
		return false, nil
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false, fmt.Errorf("process lookup: %w", err)
	}
	if runtime.GOOS == "windows" {
		// Best-effort; the Windows process signal semantics differ. Treat as running if found.
		return true, nil
	}

	if err := proc.Signal(syscall.Signal(0)); err == nil {
		return true, nil
	} else if err == os.ErrProcessDone {
		return false, nil
	} else {
		var errno syscall.Errno
		if errors.As(err, &errno) {
			if errno == syscall.ESRCH {
				return false, nil
			}
			if errno == syscall.EPERM {
				return true, nil
			}
		}
		return false, fmt.Errorf("process check: %w", err)
	}
}

func terminateProcess(pid int, timeout time.Duration) error {
	if pid <= 0 {
		return fmt.Errorf("terminate process: invalid pid %d", pid)
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("terminate process: %w", err)
	}

	if runtime.GOOS == "windows" {
		_ = proc.Kill()
		return nil
	}

	_ = proc.Signal(syscall.SIGTERM)
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		running, err := isProcessRunning(pid)
		if err == nil && !running {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}

	_ = proc.Kill()
	return nil
}
