// Command riochat is the Riochat server. Run with --local --data-dir <path> to
// start it as the single-user desktop sidecar.
//
// @version 0.4.5
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/riochat/riochat/backend/internal/app"
	"github.com/riochat/riochat/backend/internal/registry"
)

func main() {
	err := app.RunCLI(context.Background(), os.Args[1:], app.Streams{
		Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr,
	}, registry.Registered())
	if err != nil {
		fmt.Fprintln(os.Stderr, "riochat:", err)
		os.Exit(1)
	}
}
