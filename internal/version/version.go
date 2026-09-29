// Package version holds build and protocol version information.
package version

// Engine is the engine version. Overridden at build time with:
//
//	go build -ldflags "-X github.com/adnannpm/Bloomsom/internal/version.Engine=0.1.0"
var Engine = "0.1.0-dev"

// Protocol is the wire protocol version clients must match during handshake.
const Protocol = 1
