package buildassets_test

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"io"
	"testing"
	"testing/fstest"

	"github.com/grauzone-dev/sandboxed-agents/internal/buildassets"
)

func TestPackageNormalizesContextWithoutChangingManager(t *testing.T) {
	manager := []byte{0x7f, 'E', 'L', 'F', '\r', '\n', 0}
	lf := fstest.MapFS{"Containerfile": {Data: []byte("FROM base\nRUN true\n")}}
	crlf := fstest.MapFS{"Containerfile": {Data: []byte("FROM base\r\nRUN true\r\n")}}
	first, err := buildassets.Package(manager, lf)
	if err != nil {
		t.Fatal(err)
	}
	second, err := buildassets.Package(manager, crlf)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("LF and CRLF context produced different embedded assets")
	}
	archive, err := zip.NewReader(bytes.NewReader(first), int64(len(first)))
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string][]byte{"manager": manager, "context/Containerfile": []byte("FROM base\nRUN true\n")} {
		file, err := archive.Open(name)
		if err != nil {
			t.Fatal(err)
		}
		got, err := io.ReadAll(file)
		file.Close()
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("%s: got %q, want %q", name, got, want)
		}
	}
}

func TestPackageHashTracksNamesContentsAndManager(t *testing.T) {
	original, err := buildassets.Package([]byte("manager"), fstest.MapFS{"a": {Data: []byte("one\n")}})
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]struct {
		manager []byte
		context fstest.MapFS
	}{
		"manager": {[]byte("new manager"), fstest.MapFS{"a": {Data: []byte("one\n")}}},
		"content": {[]byte("manager"), fstest.MapFS{"a": {Data: []byte("two\n")}}},
		"path":    {[]byte("manager"), fstest.MapFS{"b": {Data: []byte("one\n")}}},
	}
	for name, test := range cases {
		t.Run(name, func(t *testing.T) {
			changed, err := buildassets.Package(test.manager, test.context)
			if err != nil {
				t.Fatal(err)
			}
			if sha256.Sum256(original) == sha256.Sum256(changed) {
				t.Fatal("asset change did not change hash")
			}
		})
	}
	rebuilt, err := buildassets.Package([]byte("manager"), fstest.MapFS{"a": {Data: []byte("one\n")}})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(original, rebuilt) {
		t.Fatal("unchanged assets produced different bytes")
	}
}

func TestPackageRetainsBinaryContextBytes(t *testing.T) {
	binary := []byte{'a', 0, '\r', '\n', 255}
	bundle, err := buildassets.Package([]byte("manager"), fstest.MapFS{"data.bin": {Data: binary}})
	if err != nil {
		t.Fatal(err)
	}
	archive, err := zip.NewReader(bytes.NewReader(bundle), int64(len(bundle)))
	if err != nil {
		t.Fatal(err)
	}
	file, err := archive.Open("context/data.bin")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	got, err := io.ReadAll(file)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, binary) {
		t.Fatalf("binary asset changed: %v", got)
	}
}
