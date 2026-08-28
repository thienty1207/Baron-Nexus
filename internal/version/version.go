// Package version contains the release version embedded in the Baron binary.
package version

// Value is overridden by the release build with Go -ldflags -X. Keeping a
// usable default makes source-built binaries self-describing as 0.1.12.
var Value = "0.1.12"
