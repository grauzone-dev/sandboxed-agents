package assets_test

import (
	"bytes"
	"debug/elf"
	"io/fs"
	"testing"

	"github.com/grauzone-dev/sandboxed-agents/internal/assets"
)

func TestEmbeddedManagerIsAStaticLinuxAMD64Executable(t *testing.T) {
	manager, err := assets.Manager()
	if err != nil {
		t.Fatal(err)
	}
	executable, err := elf.NewFile(bytes.NewReader(manager))
	if err != nil {
		t.Fatal(err)
	}
	defer executable.Close()
	if executable.Machine != elf.EM_X86_64 {
		t.Fatalf("machine=%v", executable.Machine)
	}
	for _, program := range executable.Progs {
		if program.Type == elf.PT_INTERP {
			t.Fatal("manager requires a dynamic loader")
		}
	}
}

func TestEmbeddedContextIsAvailableWithoutHostFiles(t *testing.T) {
	context, err := assets.Context()
	if err != nil {
		t.Fatal(err)
	}
	if err := fs.WalkDir(context, ".", func(name string, entry fs.DirEntry, err error) error { return err }); err != nil {
		t.Fatal(err)
	}
	if _, err := fs.Stat(context, ".gitkeep"); err == nil {
		t.Fatal("context contains its checkout placeholder")
	}
}
