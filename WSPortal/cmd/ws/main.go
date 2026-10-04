// Command ws is the WSPortal command-line interface.
//
// WSPortal makes a working environment portable: capture the environment you
// work in, then restore it on another computer.
package main

import (
	"fmt"
	"os"

	"github.com/cheeselord1161/WS_Portal/internal/cli"
)

func main() {
	if err := cli.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "ws: %v\n", err)
		os.Exit(1)
	}
}
