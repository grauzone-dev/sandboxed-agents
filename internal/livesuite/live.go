package livesuite

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/grauzone-dev/sandboxed-agents/internal/controllergroup"
	"github.com/grauzone-dev/sandboxed-agents/internal/platform"
	"github.com/grauzone-dev/sandboxed-agents/internal/process"
)

type Config struct {
	OptIn           bool
	Images          bool
	Commit          string
	Repository      string
	OutputDirectory string
	Host            platform.Host
	Run             process.Runner
	Stdout          io.Writer
	Stderr          io.Writer
}

type Check struct {
	Name   string `json:"name"`
	Result string `json:"result"`
}

type Summary struct {
	SchemaVersion         int     `json:"schema_version"`
	Kind                  string  `json:"kind"`
	Commit                string  `json:"commit"`
	Platform              string  `json:"platform"`
	Result                string  `json:"result"`
	ImagePartSelected     bool    `json:"image_part_selected"`
	ImagePartRan          bool    `json:"image_part_ran"`
	ImageCoverageComplete bool    `json:"image_coverage_complete"`
	Checks                []Check `json:"checks"`
}

var commitPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)
var assetLinePattern = regexp.MustCompile(`^assets [0-9a-f]{64}$`)

func Run(ctx context.Context, config Config) (result error) {
	if !config.OptIn {
		if config.Stdout != nil {
			_, result = fmt.Fprintln(config.Stdout, skippedMessage)
		}
		return result
	}
	if !commitPattern.MatchString(config.Commit) {
		return errors.New(invalidCommitMessage)
	}
	host := config.Host
	platformName := ""
	switch host.OS {
	case "linux":
		platformName = "linux"
	case "windows":
		platformName = "windows-11"
	default:
		return errors.New(unsupportedHostMessage)
	}
	summary := Summary{SchemaVersion: 1, Kind: "live-suite", Commit: config.Commit, Platform: platformName, Result: "fail", ImagePartSelected: config.Images, Checks: []Check{}}
	path := filepath.Join(config.OutputDirectory, "live-suite-"+platformName+".json")
	if err := writeSummary(path, summary); err != nil {
		return err
	}
	defer func() {
		if result == nil {
			summary.Result = "pass"
		}
		result = errors.Join(result, writeSummary(path, summary))
	}()
	group, err := controllergroup.CurrentGroup()
	if err != nil {
		return err
	}
	if group == "default" {
		return errors.New(defaultGroupMessage)
	}
	if host.Architecture != "amd64" || (host.OS == "windows" && (host.WindowsMajor != 10 || host.WindowsBuild < 22000 || !host.WindowsWorkstation)) {
		return errors.New(unsupportedHostMessage)
	}
	run := config.Run
	if run == nil {
		run = platform.Run
	}
	var dirty bytes.Buffer
	statusArgs := []string{"-C", config.Repository, "status", "--porcelain", "--untracked-files=all", "--", "."}
	absoluteRecord, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	relativeRecord, relativeErr := filepath.Rel(config.Repository, absoluteRecord)
	if relativeErr == nil && !filepath.IsAbs(relativeRecord) && relativeRecord != ".." && !strings.HasPrefix(relativeRecord, ".."+string(filepath.Separator)) {
		statusArgs = append(statusArgs, ":(exclude,literal)"+filepath.ToSlash(relativeRecord))
	}
	status, err := run(ctx, process.Request{Name: "git", Args: statusArgs, Streams: process.Streams{Stdout: &dirty, Stderr: config.Stderr}})
	if err != nil || status != 0 || dirty.Len() != 0 {
		return errors.New(dirtySourceMessage)
	}
	check := func(name string, action func() error) error {
		err := action()
		outcome := "pass"
		if err != nil {
			outcome = "fail"
		}
		summary.Checks = append(summary.Checks, Check{Name: name, Result: outcome})
		return err
	}
	directory, err := os.MkdirTemp("", "sandboxed-agents-live-*")
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, os.RemoveAll(directory)) }()
	executable := filepath.Join(directory, "sandboxed-agents")
	if host.OS == "windows" {
		executable += ".exe"
	}
	if err := check("build-host", func() error {
		status, err := run(ctx, process.Request{Name: "go", Args: []string{"-C", config.Repository, "run", "./tools/build", "-version", config.Commit, "-output", executable}, Streams: process.Streams{Stdout: config.Stdout, Stderr: config.Stderr}})
		if err != nil || status != 0 {
			return errors.New(buildHostFailureMessage)
		}
		return nil
	}); err != nil {
		return err
	}
	environment := withGroup(os.Environ(), group)
	invoke := func(args []string, stdout io.Writer) error {
		status, err := run(ctx, process.Request{Name: executable, Args: args, Env: environment, Streams: process.Streams{Stdout: stdout, Stderr: config.Stderr}})
		if err != nil {
			return err
		}
		if status != 0 {
			return fmt.Errorf("exit status %d", status)
		}
		return nil
	}
	var version bytes.Buffer
	if err := check("version", func() error {
		if err := invoke([]string{"version"}, &version); err != nil {
			return errors.New(versionFailureMessage)
		}
		lines := strings.Split(strings.TrimSpace(version.String()), "\n")
		if len(lines) != 2 || lines[0] != "sandboxed-agents "+config.Commit || !assetLinePattern.MatchString(lines[1]) {
			return errors.New(versionMismatchMessage)
		}
		return nil
	}); err != nil {
		return err
	}
	if err := check("list", func() error {
		if err := invoke([]string{"list"}, config.Stdout); err != nil {
			return errors.New(listFailureMessage)
		}
		return nil
	}); err != nil {
		return err
	}
	if config.Images {
		if err := check("images", func() error {
			if err := invoke([]string{"build"}, config.Stdout); err != nil {
				return errors.New(imagesFailureMessage)
			}
			return nil
		}); err != nil {
			return err
		}
		summary.ImagePartRan = true
	}
	return nil
}

func withGroup(environment []string, group string) []string {
	result := make([]string, 0, len(environment)+1)
	for _, value := range environment {
		key, _, _ := strings.Cut(value, "=")
		if !strings.EqualFold(key, controllergroup.GroupEnvironment) {
			result = append(result, value)
		}
	}
	return append(result, controllergroup.GroupEnvironment+"="+group)
}

func writeSummary(path string, summary Summary) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(summary, "", "  ")
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".live-summary-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err := file.Write(append(data, '\n')); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}
