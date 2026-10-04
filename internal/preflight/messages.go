package preflight

const failedMessage = "One or more required prerequisites are missing or could not be checked"
const timeoutMessage = "Host prerequisite check timed out waiting for the Podman connection"
const unsupportedMessage = "Prerequisite checks are not available for this operating system yet"

type messageText struct {
	Missing, Met, Unknown string
	Auxiliary             bool
}

var messages = map[string]messageText{
	"windows":         {Missing: "Windows 11 x64, build 22000 or later, is required; update Windows on a 64-bit PC", Met: "Windows 11 x64, build 22000 or later", Unknown: ""},
	"client":          {Missing: "Podman client %s or later is required; install or update Podman", Met: "Podman client %s or later is installed", Unknown: "Could not read the Podman client version"},
	"machine_version": {Missing: "Podman machine must run Podman %s or later; upgrade or recreate the machine", Met: "Podman machine runs Podman %s or later", Unknown: "Could not read the Podman version of the Podman machine; check the Podman connection"},
	"running_machine": {Missing: "No running Podman machine found; create one with `podman machine init` and start it with `podman machine start`", Met: "A Podman machine is running", Unknown: "Could not determine whether a Podman machine is running"},
	"wsl2":            {Missing: "Podman machine must use WSL2; enable WSL2 and recreate the machine with the WSL provider", Met: "Podman machine uses WSL2", Unknown: "Could not determine whether the Podman machine uses WSL2"},
	"rootless":        {Missing: "Podman machine must run rootless; run `podman machine set --rootful=false` and restart the machine", Met: "Podman machine runs rootless", Unknown: "Could not determine whether the Podman machine runs rootless"},
	"cgroups":         {Missing: "Podman machine must use cgroups v2 with cpu, memory and pids delegated; enable delegation of these controllers and restart the machine", Met: "cgroups v2 delegates cpu, memory and pids", Unknown: "Could not determine whether cgroups v2 delegates cpu, memory and pids in the Podman machine; check the Podman connection"},
	"ssh":             {Missing: "ssh was not found on PATH; install the Windows OpenSSH Client and add it to PATH", Met: "ssh is available on PATH", Unknown: ""},
	"ssh_keygen":      {Missing: "ssh-keygen was not found on PATH; install the Windows OpenSSH Client and add it to PATH", Met: "ssh-keygen is available on PATH", Unknown: ""},
	"ssh_keyscan":     {Missing: "ssh-keyscan was not found on PATH; install the Windows OpenSSH Client and add it to PATH", Met: "ssh-keyscan is available on PATH", Unknown: ""},
	"automount":       {Auxiliary: true, Met: "Podman machine reports where Windows drives are mounted", Unknown: "WSL automount root could not be determined from the Podman machine; this does not fail the check"},
}
