package login

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/mxschmitt/playwright-go"
)

// runPlaywright keeps direct binaries consistent with the shell launchers.
// Prefer the driver's bundled Node.js when it exists. If an installation was
// made with a preinstalled Node.js, the cache may contain the package without
// a bundled node file; retry that specific missing-node failure with the
// system node instead.
func runPlaywright() (*playwright.Playwright, error) {
	if strings.TrimSpace(os.Getenv("PLAYWRIGHT_NODEJS_PATH")) != "" {
		return playwright.Run()
	}
	pw, err := playwright.Run()
	if err == nil || !strings.Contains(err.Error(), "no such file or directory") || !strings.Contains(err.Error(), "node") {
		return pw, err
	}
	if err := ensurePlaywrightNode(); err != nil {
		return nil, err
	}
	return playwright.Run()
}

func ensurePlaywrightNode() error {
	if strings.TrimSpace(os.Getenv("PLAYWRIGHT_NODEJS_PATH")) == "" {
		if node, err := exec.LookPath("node"); err == nil {
			if err := os.Setenv("PLAYWRIGHT_NODEJS_PATH", node); err != nil {
				return fmt.Errorf("设置 Playwright Node.js 路径失败: %w", err)
			}
		}
	}
	return nil
}
