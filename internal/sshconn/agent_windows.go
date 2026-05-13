//go:build windows

package sshconn

import (
	"fmt"
	"net"

	winio "github.com/Microsoft/go-winio"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

const opensshAgentPipe = `\\.\pipe\openssh-ssh-agent`

// dialAgentPipe dials the OpenSSH agent named pipe.
// Replaced in tests to exercise both success and error paths.
var dialAgentPipe = func() (net.Conn, error) {
	return winio.DialPipe(opensshAgentPipe, nil)
}

func buildAgentAuth() ([]ssh.AuthMethod, error) {
	conn, err := dialAgentPipe()
	if err != nil {
		return nil, fmt.Errorf("connecting to OpenSSH agent (%s): %w", opensshAgentPipe, err)
	}
	return []ssh.AuthMethod{ssh.PublicKeysCallback(agent.NewClient(conn).Signers)}, nil
}
