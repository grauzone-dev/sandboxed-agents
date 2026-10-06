package cli

const (
	agentVersionMissingMessage   = "option --version needs a version"
	agentVersionDuplicateMessage = "option --version is given more than once"
	agentUnpinDuplicateMessage   = "option --unpin is given more than once"
	agentVersionInvalidFormat    = "invalid agent version %q; use an exact version such as 1.2.3"
)

const agentEnableHelp = `Usage: sandboxed-agents agents enable NAME AGENT [--version X]

Install the agent AGENT into the home volume of the running sandbox NAME and
add it to the sandbox's agent selection.

Options:
  --version X    install exactly version X of the agent's npm package and
                 pin the agent to it
  --help         show this help

Without --version, an agent that is not enabled gets the version of its npm
package that is latest at that moment, and no pin. On an agent that is
already enabled, agents enable without --version installs nothing and
reports the installed version and the pin.

X is an exact version such as 1.2.3, also with a prerelease or build
metadata such as 1.2.3-beta.1; tags such as latest and ranges such as ^1.2
are refused. With --version X on an enabled agent, a different installed
version is replaced by X and X becomes the pin, replacing an earlier pin.
When X is already installed, nothing is reinstalled and only the pin is set
to X.

The pin is stored in the home volume with the agent selection, so it
survives stop and start. agents update reinstalls the pinned version, and
agents disable removes the pin together with the agent.
`

const agentUpdateHelp = `Usage: sandboxed-agents agents update NAME AGENT [--unpin]

Reinstall the enabled agent AGENT in the running sandbox NAME: its pinned
version when a pin is set, or otherwise the version of its npm package that
is latest at that moment.

Options:
  --unpin    remove the agent's pin and install the latest version; on an
             agent without a pin, this is the same as a plain update
  --help     show this help

An agent that is not enabled is refused with a message naming
sandboxed-agents agents enable NAME AGENT. agents enable NAME AGENT
--version X sets a pin.
`
