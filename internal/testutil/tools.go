package testutil

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type ToolSpec struct {
	EnvVar      string // optional env var that points to the binary
	BinaryName  string // required (used for PATH lookup + default bin name)
	SiblingPath string // optional, repo-relative path to the binary
	BuildDir    string // optional, repo-relative dir to run `make build` when SiblingPath is missing
}

func EnsureTool(ctx context.Context, spec ToolSpec) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	spec.EnvVar = strings.TrimSpace(spec.EnvVar)
	spec.BinaryName = strings.TrimSpace(spec.BinaryName)
	spec.SiblingPath = strings.TrimSpace(spec.SiblingPath)
	spec.BuildDir = strings.TrimSpace(spec.BuildDir)

	if spec.BinaryName == "" {
		return "", errors.New("tool: binary_name required")
	}

	if spec.EnvVar != "" {
		if v := strings.TrimSpace(os.Getenv(spec.EnvVar)); v != "" {
			if fileExists(v) {
				return v, nil
			}
			return "", fmt.Errorf("tool: %s points to missing file", spec.EnvVar)
		}
	}

	if p, err := exec.LookPath(spec.BinaryName); err == nil {
		return p, nil
	}

	if spec.SiblingPath != "" {
		p := filepath.Join(repoRoot(), spec.SiblingPath)
		if fileExists(p) {
			return p, nil
		}

		if spec.BuildDir != "" {
			if err := buildSibling(ctx, spec.BuildDir); err != nil {
				return "", err
			}
			if fileExists(p) {
				return p, nil
			}
		}
	}

	return "", fmt.Errorf("tool: %s not found", spec.BinaryName)
}

func run(ctx context.Context, dir string, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	logs := io.Discard
	if os.Getenv("JUNO_TEST_LOG") != "" {
		logs = os.Stdout
	}
	cmd.Stdout = logs
	cmd.Stderr = logs
	return cmd.Run()
}

func fileExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}

func repoRoot() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "."
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func buildSibling(ctx context.Context, relDir string) error {
	dir := filepath.Join(repoRoot(), relDir)
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return fmt.Errorf("tool: build dir missing: %s", dir)
	}

	buildCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	return run(buildCtx, dir, "make", "build")
}
