package integrations

const (
	LoginNeedsTerminal = "Integration login needs an interactive terminal"
	GitHubStartFailure = "could not start GitHub CLI %s: %w"
	GitHubFailure      = "GitHub CLI %s failed with exit status %d"
	GitHubIdentity     = "integrations login github must run as UID and GID 1000"
)
