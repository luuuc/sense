//go:build linux && arm64

package embed

import _ "embed"

//go:embed bundle/linux_arm64/libonnxruntime.so
var ortLibData []byte

func loadBundledORTLib() []byte {
	return ortLibData
}
