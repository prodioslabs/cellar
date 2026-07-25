package runtime

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/prodioslabs/cellar/internal/sandbox"
)

func TestCreateAndStartWithNonRootImage(t *testing.T) {
	if os.Getenv("CELLAR_INTEGRATION") != "1" {
		t.Skip("set CELLAR_INTEGRATION=1 to run Docker integration tests")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	driver, err := NewDriver()
	if err != nil {
		t.Fatal(err)
	}
	defer driver.Close()

	agentBinary, err := filepath.Abs("../../bin/cellar-agent")
	if err != nil {
		t.Fatal(err)
	}
	sb := &sandbox.Sandbox{
		ID: "integration-curl-agent",
		Spec: sandbox.Spec{
			Image: "curlimages/curl",
			Network: sandbox.NetworkPolicy{
				Mode: sandbox.NetworkAllowlist,
			},
		},
	}

	containerID, err := driver.CreateAndStart(ctx, sb, CreateOpts{
		DataDir:     t.TempDir(),
		AgentBinary: agentBinary,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = driver.Remove(context.Background(), containerID)
	})

	inspect, err := driver.cli.ContainerInspect(ctx, containerID)
	if err != nil {
		t.Fatal(err)
	}
	if inspect.Config == nil {
		t.Fatal("container config is missing")
	}
	if inspect.Config.User != "0:0" {
		t.Fatalf("container user: got %q, want 0:0", inspect.Config.User)
	}
	if inspect.State == nil || !inspect.State.Running {
		t.Fatalf("container is not running: %#v", inspect.State)
	}
}
