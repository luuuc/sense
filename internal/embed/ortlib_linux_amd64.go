//go:build linux && amd64

package embed

import _ "embed"

//go:embed bundle/linux_amd64/libonnxruntime.so
var ortLibData []byte

func loadBundledORTLib() []byte {
	return ortLibData
}
