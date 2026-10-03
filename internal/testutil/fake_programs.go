package testutil

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

const fakeStateEnv = "SANDBOXED_AGENTS_FAKE_STATE"

var fakeProgramNames = []string{"podman", "ssh", "getent"}

type Response struct {
	Stdout    string
	Stderr    string
	ExitCode  int
	AbsentEnv []string
}

type Call struct{ Args []string }

type FakePrograms struct {
	Podman string
	SSH    string
	Getent string
	t      testing.TB
	state  string
}

func NewFakePrograms(t testing.TB) *FakePrograms {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.Mkdir(bin, 0700); err != nil {
		t.Fatal(err)
	}
	source, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	f := &FakePrograms{t: t, state: dir}
	for _, name := range fakeProgramNames {
		path := filepath.Join(bin, name)
		if runtime.GOOS == "windows" {
			path += ".exe"
		}
		if err := copyExecutable(source, path); err != nil {
			t.Fatal(err)
		}
		if name == "podman" {
			f.Podman = path
		} else if name == "ssh" {
			f.SSH = path
		} else {
			f.Getent = path
		}
	}
	t.Setenv(fakeStateEnv, dir)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return f
}

func (f *FakePrograms) Script(name string, responses ...Response) {
	f.t.Helper()
	f.program(name)
	data, err := json.Marshal(responses)
	if err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.state, name+".responses.json"), data, 0600); err != nil {
		f.t.Fatal(err)
	}
}

func (f *FakePrograms) Calls(name string) []Call {
	f.t.Helper()
	f.program(name)
	file, err := os.Open(filepath.Join(f.state, name+".calls.jsonl"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		f.t.Fatal(err)
	}
	defer file.Close()
	var calls []Call
	decoder := json.NewDecoder(file)
	for {
		var call Call
		err := decoder.Decode(&call)
		if err == io.EOF {
			return calls
		}
		if err != nil {
			f.t.Fatal(err)
		}
		calls = append(calls, call)
	}
}

func (f *FakePrograms) program(name string) {
	f.t.Helper()
	if !slices.Contains(fakeProgramNames, name) {
		f.t.Fatalf("unknown fake program %q", name)
	}
}

func copyExecutable(source, target string) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

func init() {
	state := os.Getenv(fakeStateEnv)
	name := strings.TrimSuffix(filepath.Base(os.Args[0]), ".exe")
	if state == "" || !slices.Contains(fakeProgramNames, name) {
		return
	}
	os.Exit(runFake(state, name, os.Args[1:]))
}

func runFake(state, name string, args []string) int {
	file, err := os.OpenFile(filepath.Join(state, name+".calls.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 99
	}
	err = json.NewEncoder(file).Encode(Call{Args: args})
	closeErr := file.Close()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 99
	}
	if closeErr != nil {
		fmt.Fprintln(os.Stderr, closeErr)
		return 99
	}
	path := filepath.Join(state, name+".responses.json")
	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 99
	}
	var responses []Response
	if err := json.Unmarshal(data, &responses); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 99
	}
	if len(responses) == 0 {
		fmt.Fprintln(os.Stderr, "unscripted call:", name)
		return 99
	}
	response := responses[0]
	for _, name := range response.AbsentEnv {
		if _, present := os.LookupEnv(name); present {
			fmt.Fprintln(os.Stderr, "unexpected environment variable:", name)
			return 99
		}
	}
	data, err = json.Marshal(responses[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 99
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 99
	}
	fmt.Fprint(os.Stdout, response.Stdout)
	fmt.Fprint(os.Stderr, response.Stderr)
	return response.ExitCode
}
