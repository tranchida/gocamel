package sftp

import (
	"net"
	"os"
	"sync/atomic"
	"testing"

	"github.com/pkg/sftp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// faultyCmder wraps the in-memory handler, failing Mkdir on one path and
// counting rename requests.
type faultyCmder struct {
	sftp.FileCmder
	failMkdir string
	renames   atomic.Int32
}

func (f *faultyCmder) Filecmd(r *sftp.Request) error {
	if r.Method == "Mkdir" && r.Filepath == f.failMkdir {
		// Surfaces client-side as os.ErrNotExist: "file does not exist".
		return os.ErrNotExist
	}
	if r.Method == "Rename" || r.Method == "PosixRename" {
		f.renames.Add(1)
	}
	return f.FileCmder.Filecmd(r)
}

func newInMemorySFTPClient(t *testing.T, failMkdir string) (*sftp.Client, *faultyCmder) {
	t.Helper()
	handlers := sftp.InMemHandler()
	cmder := &faultyCmder{FileCmder: handlers.FileCmd, failMkdir: failMkdir}
	handlers.FileCmd = cmder

	serverConn, clientConn := net.Pipe()
	server := sftp.NewRequestServer(serverConn, handlers)
	go func() { _ = server.Serve() }()
	client, err := sftp.NewClientPipe(clientConn, clientConn)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
	})
	return client, cmder
}

func writeRemoteFile(t *testing.T, client *sftp.Client, path string) {
	t.Helper()
	f, err := client.Create(path)
	require.NoError(t, err)
	_, err = f.Write([]byte("payload"))
	require.NoError(t, err)
	require.NoError(t, f.Close())
}

func TestMoveSFTPFile_CreatesMissingDestination(t *testing.T) {
	client, _ := newInMemorySFTPClient(t, "")
	require.NoError(t, client.Mkdir("/in"))
	writeRemoteFile(t, client, "/in/a.txt")

	moveSFTPFile(client, "/in/a.txt", "/done")

	_, err := client.Stat("/done/a.txt")
	assert.NoError(t, err)
	_, err = client.Stat("/in/a.txt")
	assert.ErrorIs(t, err, os.ErrNotExist)
}

func TestMoveSFTPFile_ExistingDestination(t *testing.T) {
	client, _ := newInMemorySFTPClient(t, "")
	require.NoError(t, client.Mkdir("/in"))
	require.NoError(t, client.Mkdir("/done"))
	writeRemoteFile(t, client, "/in/a.txt.camelExclusiveReadLock")

	moveSFTPFile(client, "/in/a.txt.camelExclusiveReadLock", "/done")

	_, err := client.Stat("/done/a.txt")
	assert.NoError(t, err)
}

// A Mkdir failure whose text merely contains "exist" ("file does not exist")
// must not be mistaken for an existing destination directory.
func TestMoveSFTPFile_DoesNotRenameWhenDestinationCannotBeCreated(t *testing.T) {
	client, cmder := newInMemorySFTPClient(t, "/done")
	require.NoError(t, client.Mkdir("/in"))
	writeRemoteFile(t, client, "/in/a.txt")

	moveSFTPFile(client, "/in/a.txt", "/done")

	assert.Zero(t, cmder.renames.Load(), "no rename must be attempted without a destination directory")
	_, err := client.Stat("/in/a.txt")
	assert.NoError(t, err, "source file must be left in place")
}
