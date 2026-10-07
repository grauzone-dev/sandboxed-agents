package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
)

func main() {
	if marker := os.Getenv("NPM_TEST_MARKER"); marker != "" {
		if err := os.WriteFile(marker, []byte("started"), 0600); err != nil {
			panic(err)
		}
	}
	if len(os.Args) > 1 && os.Args[1] == "paths" {
		fmt.Fprint(os.Stdout, os.Getenv("SANDBOXED_AGENTS_NPM_PATHS"))
		return
	}
	json.NewEncoder(os.Stdout).Encode(os.Args[1:])
	input, err := io.ReadAll(os.Stdin)
	if err != nil {
		panic(err)
	}
	fmt.Fprintf(os.Stdout, "stdin:%s", input)
	fmt.Fprint(os.Stderr, "fixture stderr\n")
	os.Exit(23)
}
