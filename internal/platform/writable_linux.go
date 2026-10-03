package platform

import "syscall"

const writeAccess = 2

func Writable(path string) bool { return syscall.Access(path, writeAccess) == nil }
