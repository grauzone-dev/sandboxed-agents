package main

const (
	flagOptInHelp     = "run the live suite against real Podman; without it the suite is skipped"
	flagImagesHelp    = "also run the image part, whose one build renews the shared images of every controller group on this host"
	flagLifecycleHelp = "also run the lifecycle part, which creates, stops, starts, and removes one sandbox in the empty controller group; it issues no build, but up may build a missing shared image"
	flagOutputHelp    = "directory for the summary file"
)
