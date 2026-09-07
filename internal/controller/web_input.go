package controller

import (
	"io"
	"os"
)

func workspaceInput() (io.ReadCloser, io.WriteCloser, error) {
	// exec.Cmd passes *os.File directly to the child. An io.Pipe instead creates
	// a stdin-copy goroutine that can block Wait after SSH exits, until the
	// browser sends more bytes or closes. The handler owns both descriptors.
	return os.Pipe()
}
