package builtinkits

import (
	"embed"
	"io/fs"
)

//go:embed all:kits
var content embed.FS

// FS returns an fs.FS rooted at the embedded kits/ directory. Subdirectories
// of the returned FS are kit names (e.g., "base").
func FS() fs.FS {
	sub, err := fs.Sub(content, "kits")
	if err != nil {
		panic(err) // unreachable: a build-time embed failure would have been caught at compile time
	}
	return sub
}
