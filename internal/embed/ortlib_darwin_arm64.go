//go:build darwin && arm64

package embed

import _ "embed"

//go:embed bundle/darwin_arm64/libonnxruntime.dylib
var ortLibData []byte

func loadBundledORTLib() []byte {
	return ortLibData
}
