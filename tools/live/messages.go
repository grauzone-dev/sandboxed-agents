package main

const (
	flagOptInHelp     = "run the live suite against real Podman; without it the suite is skipped"
	flagImagesHelp    = "also run the image part, whose one build renews the shared images of every controller group on this host"
	flagLifecycleHelp = "also run the lifecycle part, which creates, stops, starts, and removes one sandbox in the empty controller group; it issues no build, but up may build a missing shared image"
	flagSSHHelp       = "also connect over host OpenSSH before and after stop/start, then remove the sandbox, volumes, and SSH setup; Windows requires a standard account; up may build a missing shared image"
	flagUpdatesHelp   = "also verify a successful update with existing SSH setup and a forced rollback in the empty controller group; uses private untagged fixture images, issues no build, and cleans up; up or update may build a missing shared image"
	flagOutputHelp    = "directory for the summary file"
)
