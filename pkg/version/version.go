package version

import (
	"fmt"
	"os"
	"runtime/debug"
	"strings"

	"github.com/blang/semver"
)

// Version is a "vSEMVER" string, and is either populated at build-time using `--ldflags -X`, or at
// init()-time by inspecting the binary's own debug info.
var Version string

// Semver is a parsed version of the Version string.
var Semver semver.Version

func init() {
	// Prefer version number inserted at build using --ldflags, but if it's not set...
	if Version == "" {
		if v := os.Getenv("TELEPRESENCE_VERSION"); v != "" {
			Version = v
		} else if i, ok := debug.ReadBuildInfo(); ok {
			// Fall back to version info from "go get"
			Version = i.Main.Version
		} else {
			Version = "(unknown version)"
		}
	}

	if (Semver == semver.Version{}) {
		switch {
		case strings.HasPrefix(Version, "("):
			// Could be "(unknown version)" from above, or "(devel)" from
			// debug.ReadBuildInfo(), and so lets go ahead and just allow all
			// parenthesized strings as special cases.
		case strings.HasPrefix(Version, "v"):
			parsed, err := semver.Parse(Version[1:])
			if err != nil {
				panic(fmt.Errorf("binary compiled with invalid version; compiled-in version must be a %q string: %q: %w", "vSEMVER", Version, err))
			}
			Semver = parsed
		default:
			panic(fmt.Errorf("binary compiled with invalid version; compiled-in version must be a %q string: %q: missing \"v\" prefix", "vSEMVER", Version))
		}
	}
}
