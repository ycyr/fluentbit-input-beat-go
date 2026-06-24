//go:build integration

package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

const (
	testImage  = "fluentbit-input-beat-go-test"
	testMarker = "fluentbit-input-beat-go-integration-test-marker"
)

// TestMain builds the plugin image once before all integration tests run, then
// removes it on exit. The image tag is passed to each compose stack via
// BEATS_TEST_IMAGE so that `docker compose up` skips the build step.
func TestMain(m *testing.M) {
	out, err := exec.Command("docker", "build", "-t", testImage, ".").CombinedOutput()
	if err != nil {
		fmt.Fprintf(os.Stderr, "docker build failed: %v\n%s\n", err, out)
		os.Exit(1)
	}
	code := m.Run()
	exec.Command("docker", "rmi", testImage).Run() //nolint:errcheck
	os.Exit(code)
}

func TestFilebeatV5(t *testing.T) {
	t.Parallel()
	runIntegration(t, "example/integration/compose-v5.yml")
}

func TestFilebeatV6(t *testing.T) {
	t.Parallel()
	runIntegration(t, "example/integration/compose-v6.yml")
}

func TestFilebeatV7(t *testing.T) {
	t.Parallel()
	runIntegration(t, "example/integration/compose-v78.yml", "FILEBEAT_IMAGE=7.17.25")
}

func TestFilebeatV8(t *testing.T) {
	t.Parallel()
	runIntegration(t, "example/integration/compose-v78.yml", "FILEBEAT_IMAGE=8.13.4")
}

// runIntegration starts the compose stack, polls the fluent-bit service logs
// for testMarker, and tears down on exit. Each test gets an isolated compose
// project so the three versions can run in parallel without port conflicts.
func runIntegration(t *testing.T, composeFile string, extraEnv ...string) {
	t.Helper()

	project := "beat-int-" + strings.ToLower(strings.ReplaceAll(t.Name(), "/", "-"))
	env := append(os.Environ(), append([]string{"BEATS_TEST_IMAGE=" + testImage}, extraEnv...)...)

	compose := func(args ...string) *exec.Cmd {
		cmd := exec.Command("docker", append([]string{"compose", "-p", project, "-f", composeFile}, args...)...)
		cmd.Env = env
		return cmd
	}

	out, err := compose("up", "-d").CombinedOutput()
	if err != nil {
		t.Fatalf("compose up: %v\n%s", err, out)
	}
	t.Cleanup(func() {
		compose("down", "-v").Run() //nolint:errcheck
	})

	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		logs, _ := compose("logs", "fluent-bit").Output()
		if strings.Contains(string(logs), testMarker) {
			t.Logf("marker found in fluent-bit output")
			return
		}
		time.Sleep(2 * time.Second)
	}

	// Timeout: dump all logs to help diagnose.
	allLogs, _ := compose("logs").Output()
	t.Errorf("marker %q not seen in fluent-bit output within 90s\n--- stack logs ---\n%s", testMarker, allLogs)
}
