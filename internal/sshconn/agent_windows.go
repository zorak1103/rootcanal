//go:build windows

package sshconn

import (
	"fmt"

	winio "github.com/Microsoft/go-winio"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

const opensshAgentPipe = `\\.\pipe\openssh-ssh-agent`

func buildAgentAuth() ([]ssh.AuthMethod, error) {
	conn, err := winio.DialPipe(opensshAgentPipe, nil)
	if err != nil {
		return nil, fmt.Errorf("connecting to OpenSSH agent (%s): %w", opensshAgentPipe, err)
	}
	return []ssh.AuthMethod{ssh.PublicKeysCallback(agent.NewClient(conn).Signers)}, nil
}
