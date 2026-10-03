package main

import (
	"os"

	"github.com/grauzone-dev/sandboxed-agents/internal/assets"
	"github.com/grauzone-dev/sandboxed-agents/internal/cli"
)

var version = "dev"

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr, version, assets.Hash()))
}
