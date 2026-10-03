package platform

import "syscall"

func Writable(path string) bool { return syscall.Access(path, 2) == nil }
