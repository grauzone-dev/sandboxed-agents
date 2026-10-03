package main

import (
	"context"
	"os"

	"github.com/grauzone-dev/sandboxed-agents/internal/manager"
	"github.com/grauzone-dev/sandboxed-agents/internal/process"
)

var version = "dev"

func main() {
	app := manager.New(version, nil)
	os.Exit(app.Run(context.Background(), os.Args[1:], process.Streams{Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr}))
}
