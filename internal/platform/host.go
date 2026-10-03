package platform

type Host struct {
	OS                 string
	Architecture       string
	WindowsMajor       uint32
	WindowsBuild       uint32
	WindowsWorkstation bool
}
