// Command agent runs on each node and executes the control plane's orders on the local Docker daemon.
package main

import (
	"log/slog"
	"os"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	logger.Error("forgeyard agent is not implemented yet")
	os.Exit(1)
}
