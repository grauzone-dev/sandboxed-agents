package buildassets

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io/fs"
	"strings"
	"unicode/utf8"
)

func Package(manager []byte, context fs.FS) ([]byte, error) {
	var data bytes.Buffer
	archive := zip.NewWriter(&data)
	write := func(name string, content []byte) error {
		file, err := archive.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
		if err != nil {
			return err
		}
		_, err = file.Write(content)
		return err
	}
	if err := write("manager", manager); err != nil {
		return nil, err
	}
	if err := write("context/", nil); err != nil {
		return nil, err
	}
	err := fs.WalkDir(context, ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || entry.Name() == ".gitkeep" {
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("build context must contain only regular files: %s", name)
		}
		content, err := fs.ReadFile(context, name)
		if err != nil {
			return err
		}
		if utf8.Valid(content) && !bytes.ContainsRune(content, 0) {
			content = []byte(strings.ReplaceAll(string(content), "\r\n", "\n"))
		}
		return write("context/"+name, content)
	})
	if err != nil {
		return nil, err
	}
	if err := archive.Close(); err != nil {
		return nil, err
	}
	return data.Bytes(), nil
}
