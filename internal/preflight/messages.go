package preflight

const failedMessage = "One or more prerequisites are missing; fix them and run the check again"
const unsupportedMessage = "Prerequisite checks are not available for this operating system yet"

var messages = map[string][2]string{
	"windows":         {"Windows 11 x64, build 22000 or later, is required; update Windows on a 64-bit PC", "Windows 11 x64, build 22000 or later"},
	"client":          {"Podman client %s or later is required; install or update Podman", "Podman client %s or later is installed"},
	"machine_version": {"Podman machine must run Podman %s or later; upgrade or recreate the machine", "Podman machine runs Podman %s or later"},
	"running_machine": {"No running Podman machine found; create one with `podman machine init` and start it with `podman machine start`", "A Podman machine is running"},
	"wsl2":            {"Podman machine must use WSL2; enable WSL2 and recreate the machine with the WSL provider", "Podman machine uses WSL2"},
	"rootless":        {"Podman machine must run rootless; run `podman machine set --rootful=false` and restart the machine", "Podman machine runs rootless"},
	"cgroups":         {"Podman machine must use cgroups v2 with cpu, memory and pids delegated; enable delegation of these controllers and restart the machine", "cgroups v2 delegates cpu, memory and pids"},
	"ssh":             {"ssh was not found on PATH; install the Windows OpenSSH Client and add it to PATH", "ssh is available on PATH"},
	"ssh_keygen":      {"ssh-keygen was not found on PATH; install the Windows OpenSSH Client and add it to PATH", "ssh-keygen is available on PATH"},
	"automount":       {"Could not find where Windows drives are mounted in the Podman machine; check the automount settings in its /etc/wsl.conf and restart the machine", "Podman machine reports where Windows drives are mounted"},
}
