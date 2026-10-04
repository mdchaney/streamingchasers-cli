package cli

import (
	"errors"
	"os/exec"
	"runtime"
	"strings"
)

// OpenBrowser opens url in the user's browser.
func OpenBrowser(url string) error {
	// The URL is handed to a program as an argument, so anything that is
	// not plainly a web address is refused rather than passed along.
	if !strings.HasPrefix(url, "https://") && !strings.HasPrefix(url, "http://") {
		return errors.New("not a web address")
	}

	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	// The opener exits at once; waiting for it keeps it from lingering
	// as a zombie while the CLI waits for the sign-in.
	go func() { _ = cmd.Wait() }()
	return nil
}
