package testutil

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"syscall"
	"time"
)

type Process struct {
	Cmd *exec.Cmd

	stdout bytes.Buffer
	stderr bytes.Buffer
}

func StartProcess(ctx context.Context, bin string, args []string, extraEnv map[string]string) (*Process, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = append([]string{}, os.Environ()...)
	for k, v := range extraEnv {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
	}

	p := &Process{Cmd: cmd}

	var outW io.Writer = &p.stdout
	var errW io.Writer = &p.stderr
	if os.Getenv("JUNO_TEST_LOG") != "" {
		outW = io.MultiWriter(os.Stdout, outW)
		errW = io.MultiWriter(os.Stdout, errW)
	}
	cmd.Stdout = outW
	cmd.Stderr = errW

	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return p, nil
}

func (p *Process) Terminate(ctx context.Context) error {
	if p == nil || p.Cmd == nil || p.Cmd.Process == nil {
		return nil
	}

	done := make(chan error, 1)
	go func() { done <- p.Cmd.Wait() }()

	if runtime.GOOS == "windows" {
		_ = p.Cmd.Process.Kill()
	} else {
		_ = p.Cmd.Process.Signal(syscall.SIGTERM)
	}

	timeout := 5 * time.Second
	if ctx != nil {
		if dl, ok := ctx.Deadline(); ok {
			if d := time.Until(dl); d > 0 && d < timeout {
				timeout = d
			}
		}
	}

	select {
	case <-time.After(timeout):
		_ = p.Cmd.Process.Kill()
		<-done
		return nil
	case <-done:
		return nil
	}
}

func (p *Process) Logs() string {
	if p == nil {
		return ""
	}

	out := strings.TrimSpace(p.stdout.String())
	err := strings.TrimSpace(p.stderr.String())
	switch {
	case out == "" && err == "":
		return ""
	case out == "":
		return "stderr:\n" + err
	case err == "":
		return "stdout:\n" + out
	default:
		return "stdout:\n" + out + "\n\nstderr:\n" + err
	}
}
