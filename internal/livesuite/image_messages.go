package livesuite

const (
	imageHostKeysEmptyMessage         = "the image part found no public SSH host key in /etc/ssh of a sandbox it created: the sandbox did not generate its host keys into its SSH server state volume"
	imageHostKeysInvalidMessage       = "the image part read an SSH host key of a sandbox it created that has no key type or no key material"
	imageHostKeysSharedMessage        = "the sandbox without toolchains and a toolchain sandbox have the same SSH host key: the key comes from the image instead of being generated into each sandbox's SSH server state volume"
	imageRebuildNoteMessage           = "the output of build does not say that existing sandboxes keep their current image until they are updated and that list marks them as outdated"
	imageNotRebuiltMessage            = "build did not rebuild an image: its tag still names the image the image part's sandbox was created from"
	imageNotRetainedMessage           = "the image a sandbox of the image part was created from is no longer available after build: the rebuild must leave replaced images in place"
	imageInspectInvalidMessage        = "podman image inspect returned an answer the image part cannot use: it must report exactly one image with an ID and at least one layer"
	imageLayersInvalidMessage         = "podman image inspect reported an image layer without an identifier"
	imageTagMissingMessage            = "the image of a sandbox of the image part does not carry its expected tag"
	imageGroupNameMessage             = "an image of the image part carries a tag other than its expected one that contains the controller group, or a sandbox of the image part was created from an image named neither by its expected tag nor by its ID: image names never contain a controller group"
	imageSandboxInspectInvalidMessage = "podman container inspect did not report exactly one container for a sandbox of the image part"
	imageSandboxStateMessage          = "a sandbox of the image part is not running under its expected name, owner, and sandbox name labels, or does not run on the image it was created from"
	imageSshVolumeMessage             = "a sandbox of the image part does not have exactly one writable SSH server state volume of its own mounted at /etc/ssh"
	imageBaseLayersMessage            = "a toolchain image does not start with the layers of the base image it was built on"
	imageContextRetainedMessage       = "a temporary build context of the executable remained in the image part's temporary directory after a build"
	imageCleanupInventoryMessage      = "podman could not report whether a container or volume of the image part's sandboxes still exists, so the cleanup cannot tell what is left"
)
