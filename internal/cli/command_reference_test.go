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
	text := strings.ReplaceAll(string(document), "\r\n", "\n")
	for _, ending := range []struct{ name, value string }{{"LF", "\n"}, {"CRLF", "\r\n"}} {
		t.Run(ending.name, func(t *testing.T) {
			assertCommandReferenceMatchesCLI(t, strings.ReplaceAll(text, "\n", ending.value))
		})
	}
}

func assertCommandReferenceMatchesCLI(t *testing.T, document string) {
	t.Helper()
	document = strings.ReplaceAll(document, "\r\n", "\n")
	entries := referenceEntries(t, document)
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
	headings := regexp.MustCompile("(?m)^## `([^`\r\n]+)`$").FindAllStringSubmatchIndex(document, -1)
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
	options := regexp.MustCompile(`--[^\s\[\]=|]+`).FindAllString(syntax, -1)
	if strings.Contains(section, "`--help`") {
		options = append(options, "--help")
	}
	slices.Sort(options)
	return slices.Compact(options)
}

// referenceProbeCandidates collects every string literal that looks like a
// command word or an option from the packages that define the CLI parser.
// These are only candidates: probing the executable decides which of them it
// actually recognizes. The scanned directories are an assumption; when parser
// definitions move to another package, add its directory here, or options
// defined there are never probed.
func referenceProbeCandidates(t *testing.T) ([]string, []string) {
	t.Helper()
	var words, options []string
	wordPattern := regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	optionPattern := regexp.MustCompile(`^--[^\s\[\]=|]+=?$`)
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
	_, stderr, _ := runCLI(t, "ssh-ports-free", args...)
	if !strings.Contains(stderr, "Usage:") {
		return true
	}
	// A usage error does not mean the option is unknown: a recognized option
	// given without its value fails with a different message. Comparing with
	// the error for an option the parser cannot know, after replacing the
	// option's name, tells the two apart without a table of option values.
	unknown := "--reference-unknown-option"
	args[len(args)-1] = unknown
	_, unknownError, _ := runCLI(t, "ssh-ports-free", args...)
	return strings.ReplaceAll(stderr, option, unknown) != unknownError
}
