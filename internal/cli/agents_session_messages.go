package cli

const (
	agentSessionMissingSandbox = "missing sandbox name; use sandboxed-agents agents session NAME AGENT [--stop]"
	agentSessionMissingAgent   = "missing agent name; use sandboxed-agents agents session NAME AGENT [--stop]"
	agentSessionNeedsTerminal  = "agents session needs an interactive terminal; run it with standard input and standard output connected to a terminal, or use --stop, which needs none"
)

const agentSessionHelp = `Usage: sandboxed-agents agents session NAME AGENT [--stop]

Start a persistent agent session for the enabled agent AGENT in the running
sandbox NAME and attach your terminal to it, or attach to the session of
that agent when one is running.

Options:
  --stop    end the agent's session instead of starting or attaching
  --help    show this help

The session is a tmux session that runs the agent's command in /workspace.
The tmux server, the session, and the agent run as the user agent, UID and
GID 1000. Detach with the tmux key binding, Ctrl-b d by default, or close
the terminal: the agent keeps running in its session, and a later
sandboxed-agents agents session NAME AGENT attaches to it again, also from
another terminal. A session ends with its agent: when the agent exits, the
session closes, and the next agents session NAME AGENT starts a new one.

Starting a session and attaching to one need an interactive terminal on
standard input and standard output. Without one, agents session starts no
session, attaches to none, and exits with status 1.

--stop ends the agent's session and needs no terminal. When no session of
the agent is running, or the agent is not enabled, it reports that nothing
was to do and exits with status 0.

Without --stop, an agent that is not enabled is refused with a message
naming sandboxed-agents agents enable NAME AGENT. Starting a session and
--stop wait while another installation or session change holds the manager
lock; attaching to a running session does not.
`
