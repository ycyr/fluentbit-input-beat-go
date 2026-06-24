module github.com/ycyr/fluentbit-input-beat-go

go 1.26

toolchain go1.26.4

// Run `go mod tidy` after `go get` to populate exact versions + go.sum.
// fluent-bit-go has no tagged releases, so it resolves to a pseudo-version
// from the master branch.
require github.com/elastic/go-lumber v0.1.1

require (
	github.com/fluent/fluent-bit-go v0.0.0-20260616051939-71a89c3094aa
	github.com/ugorji/go/codec v1.3.1
)

require github.com/klauspost/compress v1.11.2 // indirect
