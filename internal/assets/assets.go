package assets

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	_ "embed"
	"fmt"
	"io"
	"io/fs"
)

//go:embed bundle.zip
var bundle []byte

func Hash() string {
	return fmt.Sprintf("%x", sha256.Sum256(bundle))
}

func Manager() ([]byte, error) {
	archive, err := zip.NewReader(bytes.NewReader(bundle), int64(len(bundle)))
	if err != nil {
		return nil, err
	}
	file, err := archive.Open("manager")
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return io.ReadAll(file)
}

func Context() (fs.FS, error) {
	archive, err := zip.NewReader(bytes.NewReader(bundle), int64(len(bundle)))
	if err != nil {
		return nil, err
	}
	return fs.Sub(archive, "context")
}
