// The module path is a placeholder: it changes to the public repository's address when the samples are published.
module github.com/survivalservers/studio-samples

go 1.22.0

toolchain go1.22.2

// outpost uses the Go layer of the SDK in ./sdk (cgo: it compiles the C library itself).
require github.com/survivalservers/studio-go/sdk v0.0.0

replace github.com/survivalservers/studio-go/sdk => ./sdk
