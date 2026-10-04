package login

import (
	"os"
	"os/exec"
	"testing"
)

func TestEnsurePlaywrightNodeHonorsExplicitPath(t *testing.T) {
	const explicit = "/tmp/playwright-node"
	t.Setenv("PLAYWRIGHT_NODEJS_PATH", explicit)
	if err := ensurePlaywrightNode(); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("PLAYWRIGHT_NODEJS_PATH"); got != explicit {
		t.Fatalf("PLAYWRIGHT_NODEJS_PATH = %q, want %q", got, explicit)
	}
}

func TestEnsurePlaywrightNodeUsesSystemNodeWhenUnset(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	t.Setenv("PLAYWRIGHT_NODEJS_PATH", "")
	if err := ensurePlaywrightNode(); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("PLAYWRIGHT_NODEJS_PATH"); got != node {
		t.Fatalf("PLAYWRIGHT_NODEJS_PATH = %q, want %q", got, node)
	}
}
