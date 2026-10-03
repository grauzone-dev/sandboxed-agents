package images_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/grauzone-dev/sandboxed-agents/internal/images"
	"github.com/grauzone-dev/sandboxed-agents/internal/process"
	"github.com/grauzone-dev/sandboxed-agents/internal/toolchains"
)

func TestImageTagsIdentifyToolchainSetsAndExecutableAssets(t *testing.T) {
	native, err := toolchains.Parse("native,native")
	if err != nil {
		t.Fatal(err)
	}
	if tag := images.Tag("abc123", native); tag != "localhost/sandboxed-agents:toolchains-native-abc123" {
		t.Fatalf("native tag = %q", tag)
	}
	if tag := images.Tag("abc123", toolchains.Set{}); tag != "localhost/sandboxed-agents:base-abc123" {
		t.Fatalf("base tag = %q", tag)
	}
	if images.Tag("def456", native) == images.Tag("abc123", native) {
		t.Fatal("asset hashes share a tag")
	}
}

func TestEnsureUsesCurrentToolchainImageWithoutBuilding(t *testing.T) {
	native, _ := toolchains.Parse("native")
	run := func(_ context.Context, request process.Request) (int, error) {
		switch strings.Join(request.Args, " ") {
		case "image exists localhost/sandboxed-agents:base-abc123":
			return 0, nil
		case "image inspect localhost/sandboxed-agents:base-abc123":
			fmt.Fprint(request.Streams.Stdout, `[{"Id":"sha256:base"}]`)
			return 0, nil
		case "image exists localhost/sandboxed-agents:toolchains-native-abc123":
			return 0, nil
		case "image inspect localhost/sandboxed-agents:toolchains-native-abc123":
			fmt.Fprint(request.Streams.Stdout, `[{"Id":"sha256:native","Labels":{"io.github.sandboxed-agents.base-image":"sha256:base"}}]`)
			return 0, nil
		default:
			t.Fatalf("unexpected Podman operation: %v", request.Args)
			return 0, nil
		}
	}
	reference, err := images.Ensure(context.Background(), "abc123", native, run, process.Streams{})
	if err != nil {
		t.Fatal(err)
	}
	if reference != "sha256:native" {
		t.Fatalf("image reference = %q", reference)
	}
}
