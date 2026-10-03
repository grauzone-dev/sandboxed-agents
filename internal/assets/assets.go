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

var archive, archiveError = zip.NewReader(bytes.NewReader(bundle), int64(len(bundle)))

func Hash() string {
	return fmt.Sprintf("%x", sha256.Sum256(bundle))
}

func Manager() ([]byte, error) {
	if archiveError != nil {
		return nil, archiveError
	}
	file, err := archive.Open("manager")
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return io.ReadAll(file)
}

func Context() (fs.FS, error) {
	if archiveError != nil {
		return nil, archiveError
	}
	return fs.Sub(archive, "context")
}
