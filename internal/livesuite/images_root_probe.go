package livesuite

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/grauzone-dev/sandboxed-agents/internal/process"
)

const rootImageHostKeysScript = `const fs=require('node:fs'),path=require('node:path');
const excluded=new Set(['/proc','/sys','/dev']);
function scan(directory) {
  for(const entry of fs.readdirSync(directory,{withFileTypes:true})) {
    const file=path.join(directory,entry.name);
    if(excluded.has(file)) continue;
    if(/^ssh_host_.*_key(?:[.]pub)?$/.test(entry.name)) throw Error("the base image contains an SSH host key file at "+file);
    if(entry.isDirectory()) scan(file);
  }
}
scan('/');
`

func verifyRootImageHostKeys(ctx context.Context, config Config, run process.Runner, group, baseName, imageID, connection string) (result error) {
	name := liveProbePrefix + group + "." + baseName + "-image-keys-" + strings.ToLower(rand.Text())

	exists := func(current context.Context) (bool, error) {
		if err := requireSelectedConnection(current, run, connection); err != nil {
			return false, err
		}
		status, err := run(current, process.Request{Name: "podman", Args: []string{"container", "exists", name}, Streams: process.Streams{Stderr: config.Stderr}})
		if err != nil {
			return false, err
		}
		if status == 1 {
			return false, nil
		}
		if status != 0 {
			return false, errors.New(imageRootProbeInventoryMessage)
		}
		return true, nil
	}
	present, err := exists(ctx)
	if err != nil {
		return err
	}
	if present {
		return errors.New(imageRootProbeNameConflictMessage)
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		result = errors.Join(result, func() error {
			present, err := exists(cleanup)
			if err != nil {
				return err
			}
			if !present {
				return nil
			}
			if err := requireSelectedConnection(cleanup, run, connection); err != nil {
				return err
			}
			var records []struct {
				ID, Name, Image string
				Config          struct{ Labels map[string]string }
			}
			if err := liveJSON(cleanup, run, []string{"container", "inspect", name}, &records); err != nil {
				return err
			}
			if len(records) != 1 || records[0].ID == "" || records[0].Name != name || records[0].Image != imageID || records[0].Config.Labels[liveProbeGroupLabel] != group || records[0].Config.Labels[liveProbeSandboxLabel] != baseName {
				return errors.New(imageRootProbeOwnershipMessage)
			}
			if err := requireSelectedConnection(cleanup, run, connection); err != nil {
				return err
			}
			status, err := run(cleanup, process.Request{Name: "podman", Args: []string{"rm", "--force", "--time=2", "--volumes", records[0].ID}, Streams: process.Streams{Stdout: config.Stdout, Stderr: config.Stderr}})
			if err != nil {
				return err
			}
			if status != 0 {
				return fmt.Errorf("podman rm: exit status %d", status)
			}
			remaining, err := exists(cleanup)
			if err != nil {
				return err
			}
			if remaining {
				return errors.New(imageRootProbeRetainedMessage)
			}
			return nil
		}())
	}()
	if err := requireSelectedConnection(ctx, run, connection); err != nil {
		return err
	}
	scan, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	args := []string{"run", "--rm", "--pull=never", "--name", name, "--label", liveProbeGroupLabel + "=" + group, "--label", liveProbeSandboxLabel + "=" + baseName, "--network=none", "--user=0:0", "--read-only", "--read-only-tmpfs=false", "--image-volume=ignore", "--cap-drop=all", "--cap-add=DAC_READ_SEARCH", "--security-opt=no-new-privileges", "--entrypoint=node", imageID, "-e", rootImageHostKeysScript}
	status, err := run(scan, process.Request{Name: "podman", Args: args, Streams: process.Streams{Stdout: config.Stdout, Stderr: config.Stderr}})
	if err != nil {
		return err
	}
	if status != 0 {
		return fmt.Errorf("podman run: exit status %d", status)
	}
	return nil
}
