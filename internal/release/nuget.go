package release

import (
	"archive/zip"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func NuGetFilename(tag string) string {
	return "SandboxedAgents." + strings.TrimPrefix(tag, "v") + ".nupkg"
}

func BuildNuGet(directory, tag, scriptDirectory string) error {
	files := map[string][]byte{
		"SandboxedAgents.nuspec": fmt.Appendf(nil, `<?xml version="1.0" encoding="utf-8"?>
<package xmlns="http://schemas.microsoft.com/packaging/2013/05/nuspec.xsd">
  <metadata>
    <id>SandboxedAgents</id>
    <version>%s</version>
    <authors>grauzone</authors>
    <requireLicenseAcceptance>false</requireLicenseAcceptance>
    <license type="expression">MIT</license>
    <projectUrl>https://github.com/grauzone-dev/sandboxed-agents</projectUrl>
    <description>Command package for sandboxed-agents, which runs coding agents in rootless Podman sandboxes; extract it and run tools/install-command.ps1 with PowerShell 7 to install the command for the current user.</description>
    <repository type="git" url="https://github.com/grauzone-dev/sandboxed-agents" />
  </metadata>
</package>
`, strings.TrimPrefix(tag, "v")),
		"[Content_Types].xml": []byte(`<?xml version="1.0" encoding="utf-8"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
  <Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml" />
  <Default Extension="nuspec" ContentType="application/octet-stream" />
  <Default Extension="exe" ContentType="application/octet-stream" />
  <Default Extension="ps1" ContentType="application/octet-stream" />
  <Override PartName="/tools/SHA256SUMS" ContentType="application/octet-stream" />
</Types>
`),
		"_rels/.rels": []byte(`<?xml version="1.0" encoding="utf-8"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="manifest" Type="http://schemas.microsoft.com/packaging/2010/07/manifest" Target="/SandboxedAgents.nuspec" />
</Relationships>
`),
	}
	for _, name := range []string{WindowsExecutable, ChecksumFilename} {
		contents, err := os.ReadFile(filepath.Join(directory, name))
		if err != nil {
			return err
		}
		files["tools/"+name] = contents
	}
	for _, name := range []string{"install-command.ps1", "remove-command.ps1", "command-path.ps1"} {
		contents, err := os.ReadFile(filepath.Join(scriptDirectory, name))
		if err != nil {
			return err
		}
		files["tools/"+name] = bytes.ReplaceAll(contents, []byte("\r\n"), []byte("\n"))
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	for _, name := range names {
		header := &zip.FileHeader{Name: name, Method: zip.Deflate, Modified: time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC)}
		header.SetMode(0644)
		entry, err := writer.CreateHeader(header)
		if err != nil {
			return err
		}
		if _, err := entry.Write(files[name]); err != nil {
			return err
		}
	}
	if err := writer.Close(); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(directory, NuGetFilename(tag)), archive.Bytes(), 0644)
}
