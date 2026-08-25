package main

import "runtime/debug"

// version is the release version. The release build sets it with a linker
// flag. A build from source keeps "dev", and versionString then reads the
// version that `go install` records in the build info.
var version = "dev"

// versionString returns the version to print. A release build reports the tag
// the linker flag supplied. A `go install` build reports the module version
// that Go recorded. Any other build from source reports "dev".
func versionString() string {
	if version != "dev" {
		return version
	}
	info, ok := debug.ReadBuildInfo()
	if ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return version
}
