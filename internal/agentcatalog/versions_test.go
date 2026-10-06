package agentcatalog_test

import (
	"github.com/grauzone-dev/sandboxed-agents/internal/agentcatalog"
	"testing"
)

func TestExactVersionsKeepTagsRangesAndPackageNamesOutOfPins(t *testing.T) {
	for _, version := range []string{"0.0.0", "1.2.3", "12.34.567", "1.2.3-beta.1", "1.2.3-0", "1.2.3-01alpha", "1.2.3+build.01", "1.2.3-beta.1+build.5"} {
		if !agentcatalog.IsExactVersion(version) {
			t.Errorf("exact version refused: %q", version)
		}
	}
	for _, version := range []string{"", "latest", "next", "*", "^1.2.3", "~1.2.3", ">=1.0.0", "1.2", "v1.2.3", "01.2.3", "1.02.3", "1.2.03", "1.2.3-01", "1.2.3-beta.01", "1.2.3-beta..1", "1.2.3+", "1.2.3+build..5", "1.2.3\n", " 1.2.3", "file:/workspace/pkg", "npm:other@1.2.3", "1.2.3 --script-shell=evil"} {
		if agentcatalog.IsExactVersion(version) {
			t.Errorf("inexact version accepted: %q", version)
		}
	}
}
