package livesuite

const (
	skippedMessage          = "live suite skipped: run go run ./tools/live -opt-in to run it against real Podman"
	defaultGroupMessage     = "live suite refuses the controller group default: set SANDBOXED_AGENTS_GROUP to a dedicated group, such as live"
	invalidCommitMessage    = "live suite cannot determine the commit: git did not report a full 40-character SHA for HEAD"
	unsupportedHostMessage  = "live suite runs only on Linux amd64 and on Windows 11 x64: a workstation edition with build 22000 or later"
	dirtySourceMessage      = "live suite refuses the checkout: git status failed or reported modified or untracked files; commit or remove them so the executable is built from HEAD"
	versionMismatchMessage  = "the built executable's version does not name the commit under test"
	buildHostFailureMessage = "building the executable for the commit under test failed"
	versionFailureMessage   = "sandboxed-agents version failed"
	listFailureMessage      = "sandboxed-agents list failed against Podman"
	imagesFailureMessage    = "sandboxed-agents build failed in the image part"
)
