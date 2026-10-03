package cli

const upHelp = `Usage: sandboxed-agents up NAME [--memory SIZE] [--cpus N] [--pids-limit N] [--shm-size SIZE]

Create the sandbox NAME and start it, or start it if it already exists.

Options:
  --memory SIZE      memory limit, at least 6m (default 8g)
  --cpus N           number of CPUs (default 4)
  --pids-limit N     maximum number of processes (default 2048)
  --shm-size SIZE    size of /dev/shm (default 1g)
  --help             show this help

Options follow NAME, as --option VALUE or --option=VALUE, each at most once.

Value formats (every value must be greater than zero; no sign, no exponent):
  SIZE           whole number of bytes, optionally followed by k, m, g, or t
                 in either case (powers of 1024), for example 512m or 16g;
                 at most 9223372036854775807 bytes
  --cpus N       whole number, optionally with a point and one to three
                 decimals, for example 2 or 1.5; at most 9223372036.854
  --pids-limit N whole number, at most 9223372036854775807

The limits are set when the sandbox is created and recorded on its container.
up never changes them: on an existing sandbox, a limit that differs from the
recorded value makes up fail without starting it. To change a limit, run
sandboxed-agents remove NAME, then sandboxed-agents up NAME with the new value.
`
