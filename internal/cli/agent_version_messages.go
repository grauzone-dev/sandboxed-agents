package cli

const (
	agentVersionMissingMessage   = "option --version needs a version"
	agentVersionDuplicateMessage = "option --version is given more than once"
	agentUnpinDuplicateMessage   = "option --unpin is given more than once"
	agentForceDuplicateMessage   = "option --force is given more than once"
	agentVersionInvalidFormat    = "invalid agent version %q; use an exact version such as 1.2.3"
)

const agentEnableHelp = `Usage: sandboxed-agents agents enable NAME AGENT [--version X] [--force]

Install the agent AGENT into the home volume of the running sandbox NAME and
add it to the sandbox's agent selection.

Options:
  --version X    install exactly version X of the agent's npm package and
                 pin the agent to it
  --force        end a running agent session of AGENT before X replaces the
                 installed version
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

Replacing the installed version is refused while an agent session of AGENT
is running, and the message names the session. With --force, the session is
ended first and named in the output, and then X is installed. A session of
another agent does not block the replacement. Where nothing is replaced,
--force has no effect and ends no session.

The pin is stored in the home volume with the agent selection, so it
survives stop and start. agents update reinstalls the pinned version, and
agents disable removes the pin together with the agent.
`

const agentUpdateHelp = `Usage: sandboxed-agents agents update NAME AGENT [--unpin] [--force]

Reinstall the enabled agent AGENT in the running sandbox NAME: its pinned
version when a pin is set, or otherwise the version of its npm package that
is latest at that moment.

Options:
  --unpin    remove the agent's pin and install the latest version; on an
             agent without a pin, this is the same as a plain update
  --force    end a running agent session of AGENT before the update
  --help     show this help

An agent that is not enabled is refused with a message naming
sandboxed-agents agents enable NAME AGENT. agents enable NAME AGENT
--version X sets a pin.

While an agent session of AGENT is running, agents update is refused and the
message names the session. With --force, the session is ended first and
named in the output, and then the agent is updated. A session of another
agent does not block the update.
`

const agentDisableHelp = `Usage: sandboxed-agents agents disable NAME AGENT [--force]

Remove the agent AGENT from the agent selection of the running sandbox NAME,
together with its pin, and remove its managed command. The agent's
credentials and cached data stay in the home volume.

Options:
  --force    end a running agent session of AGENT before the agent is
             disabled
  --help     show this help

While an agent session of AGENT is running, agents disable is refused and
the message names the session. With --force, the session is ended first and
named in the output, and then the agent is disabled. A session of another
agent does not block it. On an agent that is not enabled, agents disable
reports that nothing was to do.
`
