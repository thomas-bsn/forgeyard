package agent

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/pkg/sftp"

	"github.com/thomas-bsn/forgeyard/internal/agentpb"
	"github.com/thomas-bsn/forgeyard/internal/docker"
	"github.com/thomas-bsn/forgeyard/internal/tunnel"
)

// SFTP reaches an app's files through the SSH gateway: the server asks for a Tunnel stream, and the agent
// connects it to an SFTP server it starts in the app's container. That server is the agent's own binary
// (`forgeyard-agent sftp-server`), static, copied into the container once per agent version: it runs in
// any image, distroless or scratch, as the container's user, and sees the container's files as its
// processes do, volumes included. Nothing else runs in the container.

// sftpHelperDir is where the helper goes in a container; its name carries the agent binary's hash.
const sftpHelperDir = "/.forgeyard"

// ServeSFTP serves SFTP on the standard input and output, from the user's home: the helper's side.
func ServeSFTP() error {
	home := os.Getenv("HOME")
	if st, err := os.Stat(home); home == "" || err != nil || !st.IsDir() {
		home = "/"
	}
	srv, err := sftp.NewServer(struct {
		io.Reader
		io.WriteCloser
	}{os.Stdin, os.Stdout}, sftp.WithServerWorkingDirectory(home))
	if err != nil {
		return err
	}
	if err := srv.Serve(); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

// sftpHelper is the Linux binary of the agent copied into containers, and its hash.
var sftpHelper struct {
	once sync.Once
	path string
	hash string
	err  error
}

// helperBinary finds the agent's binary for Linux: $FORGEYARD_SFTP_HELPER when set, else itself on Linux,
// else (an agent run on a Mac during development) forgeyard-agent-linux-<arch> next to it.
func helperBinary() (path, hash string, err error) {
	sftpHelper.once.Do(func() {
		h := &sftpHelper
		h.path = os.Getenv("FORGEYARD_SFTP_HELPER")
		if h.path == "" {
			self, err := os.Executable()
			if err != nil {
				h.err = err
				return
			}
			h.path = self
			if runtime.GOOS != "linux" {
				h.path = filepath.Join(filepath.Dir(self), "forgeyard-agent-linux-"+runtime.GOARCH)
			}
		}
		f, err := os.Open(h.path)
		if err != nil {
			h.err = fmt.Errorf("SFTP impossible sur ce node : l'agent n'a pas de binaire Linux à copier dans le conteneur (%v)", err)
			return
		}
		defer f.Close()
		sum := sha256.New()
		if _, err := io.Copy(sum, f); err != nil {
			h.err = err
			return
		}
		h.hash = hex.EncodeToString(sum.Sum(nil))[:12]
	})
	return sftpHelper.path, sftpHelper.hash, sftpHelper.err
}

// installHelper copies the helper into a container, unless it is there already, and returns its path.
func installHelper(ctx context.Context, dc *docker.Client, container string) (string, error) {
	src, hash, err := helperBinary()
	if err != nil {
		return "", err
	}
	name := "sftp-server-" + hash
	target := sftpHelperDir + "/" + name
	if ok, err := dc.PathExists(ctx, container, target); err != nil {
		return "", err
	} else if ok {
		return target, nil
	}
	bin, err := os.ReadFile(src)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	now := time.Now()
	dir := sftpHelperDir[1:]
	if err := tw.WriteHeader(&tar.Header{Typeflag: tar.TypeDir, Name: dir + "/", Mode: 0o755, ModTime: now}); err != nil {
		return "", err
	}
	if err := tw.WriteHeader(&tar.Header{Name: dir + "/" + name, Mode: 0o755, Size: int64(len(bin)), ModTime: now}); err != nil {
		return "", err
	}
	if _, err := tw.Write(bin); err != nil {
		return "", err
	}
	if err := tw.Close(); err != nil {
		return "", err
	}
	if err := dc.PutArchive(ctx, container, "/", buf.Bytes()); err != nil {
		return "", fmt.Errorf("copie du serveur SFTP dans le conteneur (système de fichiers en lecture seule ?) : %w", err)
	}
	return target, nil
}

// openSFTP answers a TunnelOpen for SFTP: it starts the helper in the app's container, then opens the
// stream and carries it to the helper until either side ends. When the helper cannot start, the stream's
// first chunk says why, with eof set.
func openSFTP(ctx context.Context, client agentpb.AgentServiceClient, dc *docker.Client, id string, appID int64, logger *slog.Logger) {
	name := containerName(appID)
	ictx, icancel := context.WithTimeout(ctx, 2*time.Minute)
	helper, err := installHelper(ictx, dc, name)
	var p *docker.Pipe
	if err == nil {
		p, err = dc.ExecPipe(ictx, name, []string{helper, "sftp-server"})
	}
	icancel()

	sctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stream, serr := client.Tunnel(sctx)
	if serr != nil {
		logger.Warn("opening an SFTP tunnel failed", "err", serr)
		if p != nil {
			p.Close()
		}
		return
	}
	if err != nil {
		logger.Warn("starting SFTP in an app's container failed", "app_id", appID, "err", err)
		stream.Send(&agentpb.TunnelChunk{TunnelId: id, Eof: true, Data: []byte(err.Error())})
		stream.CloseSend()
		stream.Recv() // until the server has read it
		return
	}
	defer p.Close()
	if err := stream.Send(&agentpb.TunnelChunk{TunnelId: id}); err != nil {
		return
	}
	conn := tunnel.New(stream, nil, func() { stream.CloseSend() })
	defer conn.Close()
	logger.Info("sftp session", "app_id", appID)

	done := make(chan struct{})
	go func() {
		io.Copy(p, conn) // the client's requests
		p.CloseWrite()
		close(done)
	}()
	io.Copy(conn, p) // the server's answers, until it exits
	if msg := p.Stderr(); msg != "" {
		logger.Warn("sftp server output", "app_id", appID, "stderr", msg)
	}
	conn.Close()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
	}
}
