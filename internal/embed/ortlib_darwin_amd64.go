//go:build darwin && amd64

package embed

import _ "embed"

//go:embed bundle/darwin_amd64/libonnxruntime.dylib
var ortLibData []byte

func loadBundledORTLib() []byte {
	return ortLibData
}
