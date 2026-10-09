package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"time"

	"github.com/grauzone-dev/sandboxed-agents/internal/livesuite"
	"github.com/grauzone-dev/sandboxed-agents/internal/platform"
)

func main() {
	if err := execute(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func execute(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("live", flag.ContinueOnError)
	flags.SetOutput(stderr)
	optIn := flags.Bool("opt-in", false, flagOptInHelp)
	images := flags.Bool("images", false, flagImagesHelp)
	lifecycle := flags.Bool("lifecycle", false, flagLifecycleHelp)
	ssh := flags.Bool("ssh", false, flagSSHHelp)
	output := flags.String("output", ".scratch/live", flagOutputHelp)
	if err := flags.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(flags.Args(), " "))
	}
	config := livesuite.Config{OptIn: *optIn, Images: *images, Lifecycle: *lifecycle, SSH: *ssh, OutputDirectory: *output, Host: platform.CurrentHost(), Stdout: stdout, Stderr: stderr}
	if !*optIn {
		return livesuite.Run(context.Background(), config)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, time.Hour)
	defer cancel()
	query := func(args ...string) (string, error) {
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Stderr = stderr
		data, err := cmd.Output()
		return strings.TrimSpace(string(data)), err
	}
	var err error
	config.Commit, err = query("rev-parse", "HEAD")
	if err != nil {
		return err
	}
	config.Repository, err = query("rev-parse", "--show-toplevel")
	if err != nil {
		return err
	}
	return livesuite.Run(ctx, config)
}
