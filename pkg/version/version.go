package version

import (
	"fmt"
	"os"
	"runtime/debug"
	"strings"

	"github.com/blang/semver"
)

// RawVersion is a "vSEMVER" string, and is either populated at build-time using `--ldflags -X`, or
// at init()-time by inspecting the binary's own debug info.
//
// You should probably avoid using this, and instead use onf of the accessor methods.
var RawVersion string

// parseVersion is a parsed version of the RawVersion string.
var parsedVersion semver.Version


func Semver() semver.Version { return semver }
func String() semver.Version { return RawVersion }
func GitTag() string         { return RawVersion }
func DockerTag() string      { return RawVersion }

func init() {
	// Prefer version number inserted at build using --ldflags, but if it's not set...
	if RawVersion == "" {
		if v := os.Getenv("TELEPRESENCE_VERSION"); strings.HasPrefix(v, "v") {
			RawVersion = v
		} else if i, ok := debug.ReadBuildInfo(); ok {
			// Fall back to version info from "go get"
			RawVersion = i.Main.Version
		} else {
			RawVersion = "(unknown version)"
		}
	}

	switch {
	case strings.HasPrefix(RawVersion, "("):
		// Could be "(unknown version)" from above, or "(devel)" from
		// debug.ReadBuildInfo(), and so lets go ahead and just allow all
		// parenthesized strings as special cases.
	case strings.HasPrefix(RawVersion, "v"):
		parsed, err := semver.Parse(RawVersion[1:])
		if err != nil {
			panic(fmt.Errorf("binary compiled with invalid version; compiled-in version must be a %q string: %q: %w", "vSEMVER", Version, err))
		}
		Semver = parsed
	default:
		panic(fmt.Errorf("binary compiled with invalid version; compiled-in version must be a %q string: %q: missing \"v\" prefix", "vSEMVER", Version))
	}
}
