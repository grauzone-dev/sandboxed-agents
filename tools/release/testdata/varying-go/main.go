package main

import (
	"os"
	"os/exec"
)

func main() {
	command := exec.Command(os.Getenv("RELEASE_TEST_GO"), os.Args[1:]...)
	command.Stdin = os.Stdin
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	if err := command.Run(); err != nil {
		os.Exit(1)
	}
	var output, target string
	for i := 1; i+1 < len(os.Args); i++ {
		switch os.Args[i] {
		case "-output":
			output = os.Args[i+1]
		case "-goos":
			target = os.Args[i+1]
		}
	}
	if target != "windows" || output == "" {
		return
	}
	state := os.Getenv("RELEASE_TEST_VARIATION")
	variation, err := os.ReadFile(state)
	if err != nil && !os.IsNotExist(err) {
		os.Exit(1)
	}
	variation = append(variation, 'x')
	if err := os.WriteFile(state, variation, 0644); err != nil {
		os.Exit(1)
	}
	file, err := os.OpenFile(output, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		os.Exit(1)
	}
	if _, err := file.Write(variation); err != nil {
		file.Close()
		os.Exit(1)
	}
	if err := file.Close(); err != nil {
		os.Exit(1)
	}
}
