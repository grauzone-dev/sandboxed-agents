---
status: accepted
---

# Command-first CLI grammar

Commands come first and the sandbox name is the first argument after the command path: `sandboxed-agents up agent01`, `sandboxed-agents agents login agent01 codex`. The parser is an ordinary subcommand tree, and sandbox names need no reserved words.

The prototype used name-first grammar (`sandboxed-agents agent01 up`). That forced every command word, including retired ones, to be reserved as a sandbox name, gave `update` two shapes, and required special parsing to detect and explain misplaced arguments.

## Considered options

- **Name first**, as in the prototype. Its only advantage was familiarity for prototype users, and the first version is not compatible with the prototype.
- **Name as an option with a default** from an environment variable or the current directory. Rejected because it hides which sandbox a command acts on, which is risky for `remove` and `update`.

## Consequences

- Command lines written for the prototype do not work.
- Commands that act on every sandbox take `--all` in place of the name.
