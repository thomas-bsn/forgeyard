package agent

import (
	"context"
	"errors"
	"io"
	"strconv"
	"sync"

	"google.golang.org/grpc/status"

	"github.com/thomas-bsn/forgeyard/internal/agentpb"
)

// An app deployed by git push is built on its node from its repository, which the control plane keeps:
// the agent opens a Tunnel stream named after the app and the commit, reads the files as a tar archive,
// builds the image from them, and sends the build's output back, so that the push shows it.

// buildSource builds an app's image from its repository at the commit of its spec.
func (r *Reconciler) buildSource(ctx context.Context, app *agentpb.AppSpec) error {
	if r.client == nil {
		return errors.New("cet agent ne peut pas recevoir les fichiers du dépôt")
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stream, err := r.client.Tunnel(ctx)
	if err != nil {
		return err
	}
	if err := stream.Send(&agentpb.TunnelChunk{TunnelId: "source/" + strconv.FormatInt(app.GetId(), 10) + "/" + app.GetGitCommit()}); err != nil {
		return err
	}
	files := &sourceReader{stream: stream}
	err = r.dc.BuildContext(ctx, app.GetImage(), files, func(out string) {
		stream.Send(&agentpb.TunnelChunk{Data: []byte(out)})
	})
	if err == nil && files.err != nil {
		err = files.err
	}
	result := ""
	if err != nil {
		result = err.Error()
	}
	stream.Send(&agentpb.TunnelChunk{Eof: true, Data: []byte(result)})
	stream.CloseSend()
	// What is left of the files when the build stopped early, then the end of the stream once the control
	// plane has read the result; the reader's lock keeps a read of Docker's request in flight out of it.
	files.mu.Lock()
	defer files.mu.Unlock()
	for {
		if _, rerr := stream.Recv(); rerr != nil {
			break
		}
	}
	return err
}

// sourceReader reads the files the control plane sends, until its chunk with eof set.
type sourceReader struct {
	mu     sync.Mutex
	stream agentpb.AgentService_TunnelClient
	buf    []byte
	done   bool
	err    error // the stream ended before the files did
}

func (s *sourceReader) Read(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for len(s.buf) == 0 {
		if s.done {
			return 0, io.EOF
		}
		chunk, err := s.stream.Recv()
		if err != nil {
			s.done, s.err = true, errors.New("fichiers du dépôt non reçus : "+status.Convert(err).Message())
			return 0, s.err
		}
		s.buf, s.done = chunk.GetData(), chunk.GetEof()
	}
	n := copy(p, s.buf)
	s.buf = s.buf[n:]
	return n, nil
}
