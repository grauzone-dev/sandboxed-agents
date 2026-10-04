package images

import (
	"context"

	"github.com/grauzone-dev/sandboxed-agents/internal/process"
	"github.com/grauzone-dev/sandboxed-agents/internal/toolchains"
)

func Current(ctx context.Context, assetHash string, set toolchains.Set, run process.Runner, streams process.Streams) (string, bool, error) {
	exists, err := imageExists(ctx, BaseTag(assetHash), run, streams)
	if err != nil || !exists {
		return "", false, err
	}
	base, err := inspectImage(ctx, BaseTag(assetHash), run, streams)
	if err != nil {
		return "", false, err
	}
	if set.String() == "" {
		return base.ID, true, nil
	}
	tag := Tag(assetHash, set)
	exists, err = imageExists(ctx, tag, run, streams)
	if err != nil || !exists {
		return "", false, err
	}
	image, err := inspectImage(ctx, tag, run, streams)
	if err != nil {
		return "", false, err
	}
	return image.ID, image.Labels[BaseImageLabel] == base.ID, nil
}
