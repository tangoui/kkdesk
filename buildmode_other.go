//go:build !tango_release

package main

// releaseBuild: go run, go build and tests do not update themselves.
const releaseBuild = false
