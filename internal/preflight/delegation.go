package preflight

import (
	"context"
	"strings"

	"github.com/grauzone-dev/sandboxed-agents/internal/process"
)

const delegationProbe = `
missing() { printf "%s\n" "not-delegated"; exit 0; }
probe_uid=$(id -u) || exit 1
[ "$probe_uid" -gt 0 ] || exit 1
probe_unit="user@${probe_uid}.service"
probe_delegate=$(systemctl show "$probe_unit" -p Delegate --value) || exit 1
probe_state=$(systemctl show "$probe_unit" -p ActiveState --value) || exit 1
probe_group=$(systemctl show "$probe_unit" -p ControlGroup --value) || exit 1
[ "$probe_delegate" = yes ] && [ "$probe_state" = active ] || missing
case "$probe_group" in
    ""|/|*"/../"*|*"/.."|*"/./"*|*"/.") exit 1 ;;
    /*) ;;
    *) exit 1 ;;
esac
probe_dir="/sys/fs/cgroup${probe_group}"
[ -d "$probe_dir" ] && [ -w "$probe_dir" ] && [ -x "$probe_dir" ] || missing
for probe_file in cgroup.procs cgroup.threads cgroup.subtree_control; do
    [ -w "$probe_dir/$probe_file" ] || missing
done
probe_controllers=$(cat "$probe_dir/cgroup.controllers") || exit 1
for probe_controller in cpu memory pids; do
    case " $probe_controllers " in
        *" $probe_controller "*) ;;
        *) missing ;;
    esac
done
printf "%s\n" "delegated"
`

func windowsCgroupDelegation(ctx context.Context, run process.Runner, machine string) (known, delegated bool) {
	output, ok := readMachineShell(ctx, run, machine, delegationProbe)
	if !ok {
		return false, false
	}
	switch strings.TrimSpace(string(output)) {
	case "delegated":
		return true, true
	case "not-delegated":
		return true, false
	default:
		return false, false
	}
}

func readMachineShell(ctx context.Context, run process.Runner, machine, script string) ([]byte, bool) {
	quoted := "'" + strings.ReplaceAll(script, "'", "'\\''") + "'"
	return readPodman(ctx, run, "machine", "ssh", machine, "sh", "-c", quoted)
}
