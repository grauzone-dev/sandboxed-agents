package images

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/grauzone-dev/sandboxed-agents/internal/assets"
	"github.com/grauzone-dev/sandboxed-agents/internal/process"
	"github.com/grauzone-dev/sandboxed-agents/internal/toolchains"
)

type imageDetails struct {
	ID       string `json:"Id"`
	RepoTags []string
	Names    []string
	Labels   map[string]string
	Config   struct{ Labels map[string]string }
}

func Ensure(ctx context.Context, assetHash string, set toolchains.Set, run process.Runner, streams process.Streams) (string, error) {
	tag := Tag(assetHash, set)
	exists, err := imageExists(ctx, BaseTag(assetHash), run, streams)
	if err != nil {
		return "", err
	}
	if !exists {
		if err := BuildBase(ctx, assetHash, run, streams); err != nil {
			return "", err
		}
	}
	if set.String() == "" {
		return tag, nil
	}
	base, err := inspectImage(ctx, BaseTag(assetHash), run, streams)
	if err != nil {
		return "", err
	}
	currentID, current, err := currentToolchainImage(ctx, tag, base.ID, run, streams)
	if err != nil {
		return "", err
	}
	if current {
		return currentID, nil
	}
	if err := buildToolchains(ctx, assetHash, base.ID, set, []string{tag}, run, streams); err != nil {
		return "", err
	}
	built, err := inspectImage(ctx, tag, run, streams)
	if err != nil {
		return "", err
	}
	if built.Labels[BaseImageLabel] != base.ID {
		return "", fmt.Errorf("toolchain image %s changed during its build; retry up", tag)
	}
	return built.ID, nil
}

func Build(ctx context.Context, assetHash string, set toolchains.Set, run process.Runner, streams process.Streams) (result error) {
	if err := BuildBase(ctx, assetHash, run, streams); err != nil {
		return err
	}
	defer func() {
		if streams.Stdout != nil {
			_, err := fmt.Fprintln(streams.Stdout, "Existing sandboxes keep their current image until you update them; list marks them as outdated.")
			result = errors.Join(result, err)
		}
	}()
	if streams.Stdout != nil {
		if _, err := fmt.Fprintf(streams.Stdout, "Built image %s.\n", BaseTag(assetHash)); err != nil {
			return err
		}
	}
	jobs, err := discoverToolchains(ctx, assetHash, run, streams)
	if err != nil {
		return err
	}
	selected := false
	for _, job := range jobs {
		if slices.Contains(job.tags, Tag(assetHash, set)) {
			selected = true
		}
	}
	if set.String() != "" && !selected {
		jobs = append(jobs, buildJob{set: set, tags: []string{Tag(assetHash, set)}})
	}
	if len(jobs) != 0 {
		base, err := inspectImage(ctx, BaseTag(assetHash), run, streams)
		if err != nil {
			return err
		}
		var failures []string
		for _, job := range jobs {
			if err := buildToolchains(ctx, assetHash, base.ID, job.set, job.tags, run, streams); err != nil {
				failures = append(failures, job.set.String())
				result = errors.Join(result, fmt.Errorf("toolchain set %s: %w", job.set.String(), err))
			} else if streams.Stdout != nil {
				for _, tag := range job.tags {
					_, err := fmt.Fprintf(streams.Stdout, "Built image %s.\n", tag)
					result = errors.Join(result, err)
				}
			}
		}
		if len(failures) > 0 {
			if streams.Stderr != nil {
				_, err := fmt.Fprintf(streams.Stderr, "Failed toolchain sets: %s.\n", strings.Join(failures, ", "))
				result = errors.Join(result, err)
			}
			return result
		}
	}
	return result
}

type buildJob struct {
	set  toolchains.Set
	tags []string
}

func discoverToolchains(ctx context.Context, assetHash string, run process.Runner, streams process.Streams) ([]buildJob, error) {
	var found []imageDetails
	if err := query(ctx, []string{"images", "--filter", "label=" + ManagedLabel + "=true", "--filter", "label=" + AssetHashLabel + "=" + assetHash, "--format", "json"}, &found, run, streams); err != nil {
		return nil, err
	}
	var jobs []buildJob
	seen := make(map[string]bool)
	for _, candidate := range found {
		ref := candidate.ID
		if ref == "" && len(candidate.Names) > 0 {
			ref = candidate.Names[0]
		}
		if ref == "" {
			return nil, fmt.Errorf("managed image query returned an image without an ID or name")
		}
		if seen[ref] {
			continue
		}
		seen[ref] = true
		details, err := inspectImage(ctx, ref, run, streams)
		if err != nil {
			return nil, err
		}
		if details.Labels[ManagedLabel] != "true" || details.Labels[AssetHashLabel] != assetHash || details.Labels[ToolchainsLabel] == "" {
			continue
		}
		set, err := toolchains.Parse(details.Labels[ToolchainsLabel])
		if err != nil {
			return nil, fmt.Errorf("toolchains recorded on image %s: %w", ref, err)
		}
		if set.String() == "" {
			continue
		}
		tags := details.RepoTags
		if len(tags) == 0 {
			tags = candidate.Names
		}
		tags = slices.DeleteFunc(slices.Clone(tags), func(tag string) bool { return tag == "" || tag == "<none>:<none>" })
		if len(tags) == 0 {
			continue
		}
		slices.Sort(tags)
		jobs = append(jobs, buildJob{set: set, tags: slices.Compact(tags)})
	}
	slices.SortFunc(jobs, func(left, right buildJob) int { return strings.Compare(left.tags[0], right.tags[0]) })
	return jobs, nil
}

func imageExists(ctx context.Context, tag string, run process.Runner, streams process.Streams) (bool, error) {
	status, err := run(ctx, process.Request{Name: "podman", Args: []string{"image", "exists", tag}, Streams: streams})
	if err != nil {
		return false, fmt.Errorf("check image %s: %w", tag, err)
	}
	switch status {
	case 0:
		return true, nil
	case 1:
		return false, nil
	default:
		return false, fmt.Errorf("podman image exists %s failed with exit status %d", tag, status)
	}
}

func inspectImage(ctx context.Context, ref string, run process.Runner, streams process.Streams) (imageDetails, error) {
	var details []imageDetails
	if err := query(ctx, []string{"image", "inspect", ref}, &details, run, streams); err != nil {
		return imageDetails{}, err
	}
	if len(details) != 1 || details[0].ID == "" {
		return imageDetails{}, fmt.Errorf("inspect image %s: expected one image with an ID", ref)
	}
	image := details[0]
	if image.Labels == nil {
		image.Labels = image.Config.Labels
	}
	return image, nil
}

func query(ctx context.Context, args []string, answer any, run process.Runner, streams process.Streams) error {
	var output bytes.Buffer
	status, err := run(ctx, process.Request{Name: "podman", Args: args, Streams: process.Streams{Stdout: &output, Stderr: streams.Stderr}})
	if err != nil {
		return fmt.Errorf("podman %s: %w", strings.Join(args, " "), err)
	}
	if status != 0 {
		return fmt.Errorf("podman %s failed with exit status %d", strings.Join(args, " "), status)
	}
	if err := json.Unmarshal(output.Bytes(), answer); err != nil {
		return fmt.Errorf("read podman %s answer: %w", strings.Join(args, " "), err)
	}
	return nil
}

func buildToolchains(ctx context.Context, assetHash, baseID string, set toolchains.Set, tags []string, run process.Runner, streams process.Streams) (result error) {
	directory, err := os.MkdirTemp("", "sandboxed-agents-toolchains-*")
	if err != nil {
		return fmt.Errorf("create toolchain build context directory: %w", err)
	}
	defer func() {
		if err := os.RemoveAll(directory); err != nil {
			result = errors.Join(result, fmt.Errorf("remove toolchain build context directory %s: %w", directory, err))
		}
	}()
	buildContext, err := assets.Context()
	if err != nil {
		return fmt.Errorf("read embedded toolchain build context: %w", err)
	}
	if err := writeToolchainRecipes(directory, buildContext, set); err != nil {
		return err
	}
	versions, err := fs.ReadFile(buildContext, "record-versions.sh")
	if err != nil {
		return fmt.Errorf("read version recording script: %w", err)
	}
	if err := os.WriteFile(filepath.Join(directory, "record-versions.sh"), versions, 0644); err != nil {
		return fmt.Errorf("write version recording script: %w", err)
	}
	args := []string{"build", "--pull=never", "--no-cache", "--build-arg", "BASE_IMAGE=" + baseID}
	for _, tag := range tags {
		args = append(args, "--tag", tag)
	}
	args = append(args, "--label", ManagedLabel+"=true", "--label", AssetHashLabel+"="+assetHash, "--label", ToolchainsLabel+"="+set.String(), "--label", BaseImageLabel+"="+baseID, "--file", filepath.Join(directory, "Containerfile"), directory)
	status, err := run(ctx, process.Request{Name: "podman", Args: args, Streams: streams})
	if err != nil {
		return fmt.Errorf("start toolchain image build: %w", err)
	}
	if status != 0 {
		return fmt.Errorf("podman toolchain build failed with exit status %d", status)
	}
	return nil
}

func writeToolchainRecipes(directory string, buildContext fs.FS, set toolchains.Set) error {
	const header = "ARG BASE_IMAGE\nFROM ${BASE_IMAGE}\n\n"
	var combined strings.Builder
	combined.WriteString(header)
	for _, name := range set.Names() {
		recipe, err := fs.Sub(buildContext, "toolchains/"+name)
		if err != nil {
			return fmt.Errorf("read %s build context: %w", name, err)
		}
		contents, err := fs.ReadFile(recipe, "Containerfile.fragment")
		if err != nil {
			return fmt.Errorf("read %s recipe: %w", name, err)
		}
		if err := os.CopyFS(filepath.Join(directory, name), recipe); err != nil {
			return fmt.Errorf("write %s build context: %w", name, err)
		}
		combined.Write(contents)
		combined.WriteString("\n")
	}
	combined.WriteString("COPY record-versions.sh /tmp/record-versions.sh\nRUN sh /tmp/record-versions.sh && rm /tmp/record-versions.sh\n")
	if err := os.WriteFile(filepath.Join(directory, "Containerfile"), []byte(combined.String()), 0644); err != nil {
		return fmt.Errorf("write combined toolchain recipe: %w", err)
	}
	return nil
}
