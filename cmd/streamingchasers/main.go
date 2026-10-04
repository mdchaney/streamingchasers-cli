// Command streamingchasers is the command line for Streaming Chasers.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"

	"github.com/mdchaney/streamingchasers-api/internal/cli"
	"golang.org/x/term"
)

// version is set when a release is built:
//
//	go build -ldflags "-X main.version=1.2.3" ./cmd/streamingchasers
var version = ""

func buildVersion() string {
	if version != "" {
		return version
	}
	// A build made with "go install module@version" knows its version.
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	stdin := int(os.Stdin.Fd())
	app := &cli.App{
		In:          os.Stdin,
		Out:         os.Stdout,
		Err:         os.Stderr,
		Getenv:      os.Getenv,
		OpenBrowser: cli.OpenBrowser,
		Interactive: term.IsTerminal(stdin),
		Progress:    term.IsTerminal(int(os.Stderr.Fd())),
		Version:     buildVersion(),
		ReadSecret: func(prompt string) (string, error) {
			fmt.Fprint(os.Stderr, prompt)
			secret, err := term.ReadPassword(stdin)
			fmt.Fprintln(os.Stderr)
			return string(secret), err
		},
	}

	code := app.Run(ctx, os.Args[1:])
	stop()
	os.Exit(code)
}
