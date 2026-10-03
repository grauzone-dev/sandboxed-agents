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
	Name string
	Args []string
	// User is the identity the process runs as; nil keeps the caller's. Only the Unix runner supports it.
	User *Identity
	// Dir is the working directory; empty keeps the caller's.
	Dir string
	// Env is the complete environment of the process; nil inherits the caller's environment.
	Env     []string
	Streams Streams
}

// Identity is a numeric Unix user and group ID.
type Identity struct {
	UID uint32
	GID uint32
}

type Runner func(context.Context, Request) (int, error)
