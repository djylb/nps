package version

import (
	"fmt"

	"github.com/djylb/tunnelwire"
)

// VERSION is the release of nps and npc.
const VERSION = "0.35.0"

// PrintVersion prints the release and the protocol version core.
func PrintVersion(core string) {
	fmt.Printf("Version: %s\nCore version: %s\nSame core version of client and server can connect each other\n", VERSION, core)
}

// Compare compares two dotted versions numerically segment by segment.
// A leading "v" and build metadata are ignored; a pre-release suffix sorts
// before the release with the same numbers.
func Compare(a, b string) int { return tunnelwire.CompareVersions(a, b) }
