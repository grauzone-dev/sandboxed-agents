//go:build !linux

package platform

func Writable(path string) bool { return false }
