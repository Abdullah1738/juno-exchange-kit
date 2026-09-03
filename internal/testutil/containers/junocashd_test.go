package containers

import "testing"

func TestNewJunocashdRequestUsesPrebuiltImage(t *testing.T) {
	req := newJunocashdRequest("0.9.13", "rpcuser", "rpcpass", " local/junocashd:test ", true)

	if req.Image != "local/junocashd:test" {
		t.Fatalf("Image = %q, want prebuilt image", req.Image)
	}
	if req.FromDockerfile.Context != "" {
		t.Fatalf("FromDockerfile.Context = %q, want empty when image is provided", req.FromDockerfile.Context)
	}
	if req.FromDockerfile.BuildLogWriter != nil {
		t.Fatalf("BuildLogWriter should not be set when image is provided")
	}
	if req.LogConsumerCfg == nil {
		t.Fatalf("LogConsumerCfg should be set when logging is enabled")
	}
}

func TestNewJunocashdRequestBuildsDockerfileByDefault(t *testing.T) {
	req := newJunocashdRequest("0.9.13", "rpcuser", "rpcpass", "", false)

	if req.Image != "" {
		t.Fatalf("Image = %q, want empty for Dockerfile build", req.Image)
	}
	if req.FromDockerfile.Context == "" {
		t.Fatalf("FromDockerfile.Context should be set by default")
	}
	if req.FromDockerfile.Dockerfile != "docker/junocashd/Dockerfile" {
		t.Fatalf("FromDockerfile.Dockerfile = %q", req.FromDockerfile.Dockerfile)
	}
}
