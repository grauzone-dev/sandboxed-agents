package integrations

const (
	AzureStartFailure      = "could not start Azure CLI login: %w"
	AzureFailure           = "Azure CLI login failed with exit status %d"
	AzureIdentity          = "integrations login azure must run as UID and GID 1000"
	AzureToolchainRequired = "Azure login requires the azure toolchain; run sandboxed-agents update %s --with %s"
)
