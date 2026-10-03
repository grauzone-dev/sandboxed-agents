package process

import (
	"context"
	"io"
)

type Streams struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

type Request struct {
	Name    string
	Args    []string
	Streams Streams
}

type Runner func(context.Context, Request) (int, error)
