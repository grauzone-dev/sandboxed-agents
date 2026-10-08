package livesuite

import (
	"encoding/json"
	"strings"

	"github.com/grauzone-dev/sandboxed-agents/internal/agentcatalog"
	"github.com/grauzone-dev/sandboxed-agents/internal/toolchains"
)

const imageIdentityScript = "set -euo pipefail\ntest \"$(id -u)\" = 1000\ntest \"$(id -g)\" = 1000\ntest \"$(id -un)\" = agent\ntest \"$(id -u agent)\" = 1000\ntest \"$(id -g agent)\" = 1000\n"

func smokeCheckScript(definition toolchains.Definition) string {
	script := imageIdentityScript + definition.SmokeCheck + "\n"
	switch definition.Name {
	case "dotnet":
		script = imageIdentityScript + "sdks=$(" + definition.SmokeCheck + ")\n" + `
printf '%s\n' "$sdks"
for series in 8 9 10; do
  printf '%s\n' "$sdks" | grep -Eq "^${series}[.]0[.][0-9]+[[:space:]]+\\["
done
`
	case "azure":
		script = imageIdentityScript + definition.SmokeCheck + ` | node -e '
let text="";
process.stdin.setEncoding("utf8");
process.stdin.on("data",part=>text+=part);
process.stdin.on("end",()=>{
  const versions=JSON.parse(text);
  if(typeof versions["azure-cli"]!=="string" || !versions["azure-cli"] ||
     typeof versions.extensions?.["azure-devops"]!=="string" || !versions.extensions["azure-devops"]) process.exit(1);
  process.stdout.write(text);
});'
`
	}
	return script
}

var baseImageCheckScript = imageIdentityScript + `
. /etc/os-release
test "$ID" = debian
test "$VERSION_ID" = 12
getconf GNU_LIBC_VERSION
for package in openssh-client openssh-server git gh tmux curl jq ripgrep less unzip bash coreutils findutils procps util-linux tar xz-utils ca-certificates gnupg; do
  test "$(dpkg-query -W -f='${Status}' "$package")" = 'install ok installed'
done
for command in ssh ssh-keygen git gh tmux curl jq rg less unzip bash find ps runuser tar xz node npm; do
  command -v "$command"
done
test -x /usr/sbin/sshd
test -x /usr/local/bin/sandboxed-agents-manager
/usr/local/bin/sandboxed-agents-manager version
test -r /usr/local/etc/sandboxed-agents/sshd_config
test -d /workspace
test -w /workspace
test -s /usr/local/share/sandboxed-agents/versions.tsv
node --version | grep -Eq '^v24[.]'
npm --version
for command in cc gcc g++ make cmake pkg-config ninja dotnet playwright az; do
  if command -v "$command" >/dev/null 2>&1; then exit 1; fi
done
for path in /usr/share/dotnet /opt/az /opt/playwright /opt/playwright-browsers /usr/local/share/sandboxed-agents/smoke/native.sh; do
  test ! -e "$path"
done
` + absentAgentsScript()

func absentAgentsScript() string {
	catalog := agentcatalog.Embedded()
	var packages []string
	var script strings.Builder
	for _, name := range catalog.Names() {
		entry, _ := catalog.Find(name)
		packages = append(packages, entry.Install.Package)
		script.WriteString("if command -v " + entry.Command + " >/dev/null 2>&1; then exit 1; fi\n")
	}
	data, _ := json.Marshal(packages)
	script.WriteString("node <<'SANDBOXED_AGENTS_BASE_JS'\n")
	script.WriteString("const fs=require('node:fs'),cp=require('node:child_process'),path=require('node:path');\n")
	script.WriteString("const packages=" + string(data) + ";\n")
	script.WriteString(`const roots=[cp.execFileSync('npm',['root','--global'],{encoding:'utf8'}).trim(),'/home/agent/.local/lib/node_modules'];
for(const root of roots) for(const name of packages) if(fs.existsSync(path.join(root,name))) process.exit(1);
SANDBOXED_AGENTS_BASE_JS
`)
	return script.String()
}

const hostKeysCheckScript = imageIdentityScript + `node <<'SANDBOXED_AGENTS_KEYS_JS'
const fs=require('node:fs'),path=require('node:path');
const excluded=new Set(['/proc','/sys','/dev','/etc/ssh']);
function scan(directory) {
  let entries;
  try { entries=fs.readdirSync(directory,{withFileTypes:true}); }
  catch(error) { if(error.code==='EACCES') return; throw error; }
  for(const entry of entries) {
    const file=path.join(directory,entry.name);
    if(excluded.has(file)) continue;
    if(/^ssh_host_.*_key(?:[.]pub)?$/.test(entry.name)) throw Error('found an SSH host key file outside the SSH server state volume: the image must not contain host keys');
    if(entry.isDirectory()) scan(file);
  }
}
scan('/');
const keys={};
for(const name of fs.readdirSync('/etc/ssh')) {
  if(!/^ssh_host_.*_key[.]pub$/.test(name)) continue;
  const parts=fs.readFileSync(path.join('/etc/ssh',name),'utf8').trim().split(/\s+/);
  if(parts.length<2 || !/^ssh-|^ecdsa-/.test(parts[0]) || !/^[A-Za-z0-9+/]+={0,2}$/.test(parts[1])) throw Error('an SSH public host key in /etc/ssh has no valid key type or key material');
  keys[name]=parts[0]+' '+parts[1];
}
if(!Object.keys(keys).length) throw Error('found no SSH public host key in /etc/ssh: the sandbox did not generate its host keys');
process.stdout.write(JSON.stringify(keys));
SANDBOXED_AGENTS_KEYS_JS
`
