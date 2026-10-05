package integrations

const (
	AzdoTokenPrompt        = "Azure DevOps personal access token: "
	AzdoTokenRequired      = "no Azure DevOps personal access token on standard input; pipe the token or run the command in a terminal"
	AzdoTokenReadFailure   = "could not read the Azure DevOps personal access token"
	AzdoInvalidToken       = "invalid Azure DevOps personal access token; enter exactly one non-empty token without spaces, line breaks, or NUL characters"
	AzdoUnknownWorkflow    = "unknown login workflow for azdo; valid workflows: pat"
	AzdoUnexpectedArgument = "integrations login azdo takes no token argument or option; enter the token at the prompt or on standard input"
	AzdoIdentity           = "integrations login azdo must run as UID and GID 1000"
	AzdoStartFailure       = "could not start Azure DevOps %s"
	AzdoFailure            = "Azure DevOps %s failed with exit status %d"
	AzdoRunFailure         = "could not run the Azure DevOps login workflow"
	AzdoToolchainRequired  = "Azure DevOps login requires the azure toolchain; run sandboxed-agents update %s --with %s"
)
