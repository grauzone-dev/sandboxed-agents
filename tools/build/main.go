package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/grauzone-dev/sandboxed-agents/internal/buildassets"
)

func main() {
	if err := build(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func build() error {
	target := flag.String("goos", runtime.GOOS, "target host operating system: linux or windows")
	output := flag.String("output", "", "host executable path (default .scratch/sandboxed-agents, with .exe for windows)")
	version := flag.String("version", "dev", "version reported by the host executable")
	flag.Parse()
	if flag.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(flag.Args(), " "))
	}
	if *target != "linux" && *target != "windows" {
		return fmt.Errorf("unsupported host operating system %q: use linux or windows", *target)
	}
	if strings.ContainsAny(*version, " \t\r\n\"'\\") || *version == "" {
		return fmt.Errorf("-version must be nonempty and contain no whitespace, quotes, or backslashes")
	}
	if *output == "" {
		*output = filepath.Join(".scratch", "sandboxed-agents")
		if *target == "windows" {
			*output += ".exe"
		}
	}
	if _, err := os.Stat("go.mod"); err != nil {
		return fmt.Errorf("run the build tool from the repository root: %w", err)
	}
	temp, err := os.MkdirTemp("", "sandboxed-agents-build-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temp)
	managerPath := filepath.Join(temp, "manager")
	if err := compile("linux", managerPath, "./cmd/sandboxed-agents-manager", ""); err != nil {
		return err
	}
	manager, err := os.ReadFile(managerPath)
	if err != nil {
		return err
	}
	bundle, err := buildassets.Package(manager, os.DirFS("build/context"))
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join("internal", "assets", "bundle.zip"), bundle, 0644); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(*output), 0755); err != nil {
		return err
	}
	return compile(*target, *output, "./cmd/sandboxed-agents", *version)
}

func compile(target, output, pkg, version string) error {
	args := []string{"build", "-trimpath", "-buildvcs=false", "-o", output}
	if version != "" {
		args = append(args, "-ldflags=-X main.version="+version)
	}
	args = append(args, pkg)
	command := exec.Command("go", args...)
	command.Env = buildEnvironment(target)
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	if err := command.Run(); err != nil {
		return fmt.Errorf("build %s for %s: %w", pkg, target, err)
	}
	return nil
}

func buildEnvironment(target string) []string {
	env := make([]string, 0, len(os.Environ())+6)
	for _, value := range os.Environ() {
		key, _, _ := strings.Cut(value, "=")
		switch strings.ToUpper(key) {
		case "GOOS", "GOARCH", "CGO_ENABLED", "GOAMD64", "GOPROXY", "GOSUMDB", "GOFLAGS", "GOEXPERIMENT", "GOWORK", "GOTOOLCHAIN", "GO111MODULE":
		default:
			env = append(env, value)
		}
	}
	return append(env, "GOOS="+target, "GOARCH=amd64", "CGO_ENABLED=0", "GOAMD64=v1", "GOPROXY=off", "GOSUMDB=off", "GOFLAGS=", "GOEXPERIMENT=", "GOWORK=off", "GOTOOLCHAIN=local", "GO111MODULE=on")
}
