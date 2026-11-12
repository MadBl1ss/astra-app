//go:build windows

package ipc

import (
	"context"
	"net"
	"strings"

	"github.com/Microsoft/go-winio"
)

// pipeSDDL is the access list of the channel: SYSTEM and administrators get
// everything, interactive users may read and write. A pipe is never reachable
// over the network with this list (no ACE for NETWORK or ANONYMOUS).
//
// The owner ACE is for `strelkad run` in a console: every accepted client
// needs a new pipe instance, and creating one is checked against this same
// list. A developer logged in over SSH is not "interactive", so without the
// owner ACE the daemon could not serve its own pipe.
const pipeSDDL = "D:P(A;;GA;;;SY)(A;;GA;;;BA)(A;;GRGW;;;IU)(A;;GA;;;OW)"

// pipeBuffer is the kernel buffer of each direction. A full State document is
// about 30 KB; one buffer holds a couple of them before a writer blocks.
const pipeBuffer = 64 * 1024

// Address turns a short channel name into what the OS wants. A full pipe
// path passes through untouched.
func Address(name string) string {
	if strings.HasPrefix(name, `\\`) {
		return name
	}
	return `\\.\pipe\` + name
}

// Listen opens the channel. It fails if a pipe with this name already
// exists, which is how a second daemon notices the first.
func Listen(name string) (net.Listener, error) {
	return winio.ListenPipe(Address(name), &winio.PipeConfig{
		SecurityDescriptor: pipeSDDL,
		InputBufferSize:    pipeBuffer,
		OutputBufferSize:   pipeBuffer,
	})
}

func dial(ctx context.Context, name string) (net.Conn, error) {
	return winio.DialPipeContext(ctx, Address(name))
}
