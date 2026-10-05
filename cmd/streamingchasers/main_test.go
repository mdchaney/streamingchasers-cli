package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mdchaney/streamingchasers-api/internal/fakeserver"
)

// binary is the command, built once for the tests of this package.
var binary string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "streamingchasers")
	if err != nil {
		panic(err)
	}
	binary = filepath.Join(dir, "streamingchasers")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	build := exec.Command("go", "build", "-ldflags", "-X main.version=1.2.3", "-o", binary, ".")
	if out, err := build.CombinedOutput(); err != nil {
		os.RemoveAll(dir)
		panic("the command does not build: " + err.Error() + "\n" + string(out))
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// run runs the built command the way a shell would, away from a terminal.
func run(t *testing.T, env []string, stdin string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	cmd := exec.Command(binary, args...)
	cmd.Env = append([]string{"STREAMINGCHASERS_CONFIG_DIR=" + filepath.Join(t.TempDir(), "streamingchasers"), "HOME=" + t.TempDir()}, env...)
	cmd.Stdin = strings.NewReader(stdin)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut

	err := cmd.Run()
	var exit *exec.ExitError
	switch {
	case errors.As(err, &exit):
		code = exit.ExitCode()
	case err != nil:
		t.Fatal(err)
	}
	return out.String(), errOut.String(), code
}

func TestVersionIsSetByTheBuild(t *testing.T) {
	stdout, _, code := run(t, []string{"STREAMINGCHASERS_HOST=http://127.0.0.1:1"}, "", "version")
	if code != 0 || !strings.HasPrefix(stdout, "streamingchasers 1.2.3 (API ") {
		t.Errorf("exit %d: %q", code, stdout)
	}
}

func TestBuildVersion(t *testing.T) {
	defer func(v string) { version = v }(version)
	version = "9.9.9"
	if got := buildVersion(); got != "9.9.9" {
		t.Errorf("got %q", got)
	}
	version = ""
	if got := buildVersion(); got == "" {
		t.Error("a build always has a version, if only dev")
	}
}

func TestTheCommandAgainstAServer(t *testing.T) {
	server := fakeserver.New()
	defer server.Close()
	env := []string{"STREAMINGCHASERS_HOST=" + server.URL, "STREAMINGCHASERS_CONFIG_DIR=" + filepath.Join(t.TempDir(), "streamingchasers")}

	_, stderr, code := run(t, env, "", "companies", "list")
	if code != 3 || !strings.Contains(stderr, "not signed in") {
		t.Errorf("exit %d: %s", code, stderr)
	}
	_, stderr, code = run(t, env, "floyd-api-token\n", "auth", "login", "--with-token")
	if code != 0 || !strings.Contains(stderr, "Signed in") {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	stdout, stderr, code := run(t, env, "", "works", "list", "--company", "Frivolous Music")
	if code != 0 || !strings.Contains(stdout, "Drunken Daisy") {
		t.Errorf("exit %d: %s%s", code, stdout, stderr)
	}
	if stderr != "" {
		t.Errorf("standard error = %q", stderr)
	}
	_, stderr, code = run(t, env, "", "works", "delete", "W-999", "-c", "2")
	if code != 2 || !strings.Contains(stderr, "pass --yes") {
		t.Errorf("exit %d: %s", code, stderr)
	}
	_, stderr, code = run(t, env, "", "works", "get", "W-404", "-c", "2")
	if code != 5 || !strings.Contains(stderr, "Not found") {
		t.Errorf("exit %d: %s", code, stderr)
	}
	stdout, _, code = run(t, []string{"STREAMINGCHASERS_HOST=" + server.URL, "STREAMINGCHASERS_TOKEN=crump-api-token"}, "", "companies", "list", "-o", "jsonl")
	if code != 0 || strings.Count(stdout, "\n") != 1 || !strings.Contains(stdout, "Frivolous Music") {
		t.Errorf("exit %d: %s", code, stdout)
	}
}
