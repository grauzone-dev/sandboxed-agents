package cli_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/grauzone-dev/sandboxed-agents/internal/testutil"
)

func TestCommandReferenceMatchesCLI(t *testing.T) {
	document, err := os.ReadFile(filepath.Join("..", "..", "docs", "command-reference.md"))
	if err != nil {
		t.Fatalf("the command reference must document the shipped CLI: %v", err)
	}
	entries := referenceEntries(t, string(document))
	words, options := referenceProbeCandidates(t)
	fakes := testutil.NewFakePrograms(t)
	fakes.Script("podman", testutil.Response{RepeatForArgs: []string{"--version"}, ExitCode: 99, Stderr: "offline reference probe\n"})
	var commands []string
	var discover func([]string)
	discover = func(prefix []string) {
		for _, word := range words {
			path := append(slices.Clone(prefix), word)
			_, stderr, _ := runCLI(t, "ssh-ports-free", append(slices.Clone(path), "%reference-probe%")...)
			if !strings.Contains(stderr, "Usage: sandboxed-agents "+strings.Join(path, " ")+"\n") {
				continue
			}
			if strings.Contains(stderr, "unknown command \"%reference-probe%\"") {
				discover(path)
			} else {
				commands = append(commands, strings.Join(path, " "))
			}
		}
	}
	discover(nil)
	slices.Sort(commands)
	for _, command := range commands {
		if _, ok := entries[command]; !ok {
			t.Errorf("command reference is missing CLI command %q", command)
		}
	}
	for command, section := range entries {
		if !slices.Contains(commands, command) {
			t.Errorf("command reference names unknown CLI command %q", command)
			continue
		}
		t.Run(command, func(t *testing.T) {
			documented := referenceSyntaxOptions(t, command, section)
			_, usageError, _ := runCLI(t, "ssh-ports-free", append(strings.Fields(command), "%reference-probe%")...)
			for _, line := range strings.Split(usageError, "\n") {
				if strings.HasPrefix(line, "Usage:") && !strings.Contains(section, "`"+line+"`") {
					t.Errorf("%s does not show the emitted usage line %q", command, line)
				}
			}
			candidates := slices.Clone(options)
			for _, option := range documented {
				if !slices.Contains(candidates, option) {
					candidates = append(candidates, option)
				}
			}
			for _, option := range candidates {
				accepted := referenceOptionAccepted(t, command, option)
				listed := slices.Contains(documented, option)
				if accepted && !listed {
					t.Errorf("command reference is missing %s option %s", command, option)
				}
				if listed && !accepted {
					t.Errorf("command reference names rejected %s option %s", command, option)
				}
				if option == "--help" && accepted {
					help, _, _ := runCLI(t, "ssh-ports-free", append(strings.Fields(command), "--help")...)
					usage, _, _ := strings.Cut(help, "\n\n")
					syntax := strings.Join(strings.Fields(strings.TrimPrefix(usage, "Usage: ")), " ")
					if syntax == "" || !strings.Contains(section, syntax) {
						t.Errorf("%s syntax does not match help usage %q", command, syntax)
					}
				}
			}
		})
	}
}

func referenceEntries(t *testing.T, document string) map[string]string {
	t.Helper()
	headings := regexp.MustCompile("(?m)^## `([a-z][a-z -]*)`$").FindAllStringSubmatchIndex(document, -1)
	entries := make(map[string]string)
	for i, heading := range headings {
		name := document[heading[2]:heading[3]]
		if _, exists := entries[name]; exists {
			t.Fatalf("duplicate command reference entry %q", name)
		}
		end := len(document)
		if i+1 < len(headings) {
			end = headings[i+1][0]
		}
		entries[name] = document[heading[1]:end]
	}
	if len(entries) == 0 {
		t.Fatal("command reference contains no command entries")
	}
	return entries
}

func referenceSyntaxOptions(t *testing.T, command, section string) []string {
	t.Helper()
	_, syntax, ok := strings.Cut(section, "```text\n")
	if !ok {
		t.Fatalf("%s has no text block showing command syntax", command)
	}
	syntax, _, ok = strings.Cut(syntax, "```")
	if !ok || !strings.Contains(syntax, "sandboxed-agents "+command) {
		t.Fatalf("%s has no complete command syntax", command)
	}
	options := regexp.MustCompile(`--[a-z][a-z-]*`).FindAllString(syntax, -1)
	if strings.Contains(section, "`--help`") {
		options = append(options, "--help")
	}
	slices.Sort(options)
	return slices.Compact(options)
}

func referenceProbeCandidates(t *testing.T) ([]string, []string) {
	t.Helper()
	var words, options []string
	wordPattern := regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	optionPattern := regexp.MustCompile(`^--[a-z][a-z-]*=?$`)
	for _, directory := range []string{".", "../sandbox", "../integrations"} {
		files, err := filepath.Glob(filepath.Join(directory, "*.go"))
		if err != nil {
			t.Fatal(err)
		}
		for _, filename := range files {
			if strings.HasSuffix(filename, "_test.go") {
				continue
			}
			file, err := parser.ParseFile(token.NewFileSet(), filename, nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			ast.Inspect(file, func(node ast.Node) bool {
				if _, importing := node.(*ast.ImportSpec); importing {
					return false
				}
				literal, ok := node.(*ast.BasicLit)
				if !ok || literal.Kind != token.STRING {
					return true
				}
				value, err := strconv.Unquote(literal.Value)
				if err != nil {
					t.Fatal(err)
				}
				if wordPattern.MatchString(value) {
					words = append(words, value)
				}
				if optionPattern.MatchString(value) {
					options = append(options, strings.TrimSuffix(value, "="))
				}
				return true
			})
		}
	}
	slices.Sort(words)
	slices.Sort(options)
	return slices.Compact(words), slices.Compact(options)
}

func referenceOptionAccepted(t *testing.T, command, option string) bool {
	t.Helper()
	if command == "agents run" {
		return false
	}
	args := strings.Fields(command)
	switch command {
	case "version", "list", "build":
	case "integrations config":
		args = append(args, "agent01", "git", "identity")
	case "integrations login":
		args = append(args, "agent01", "github")
	default:
		args = append(args, "agent01")
		if strings.HasPrefix(command, "agents ") {
			args = append(args, "codex")
		}
	}
	if command == "update" && option == "--all" {
		args[len(args)-1] = "--all"
	} else {
		args = append(args, option)
	}
	switch option {
	case "--memory", "--shm-size":
		args = append(args, "8g")
	case "--cpus", "--pids-limit":
		args = append(args, "4")
	case "--port":
		args = append(args, "2222")
	case "--with":
		args = append(args, "none")
	case "--agents":
		args = append(args, "codex")
	case "--version":
		args = append(args, "1.2.3")
	case "--name", "--email":
		args = append(args, "reference@example.test")
	}
	_, stderr, _ := runCLI(t, "ssh-ports-free", args...)
	return !strings.Contains(stderr, "Usage:")
}
