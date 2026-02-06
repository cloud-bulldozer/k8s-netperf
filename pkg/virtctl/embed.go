package virtctl

import (
	_ "embed"
)

//go:embed binaries/linux/amd64/virtctl
var virtctlLinuxAmd64 []byte
