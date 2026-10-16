package api

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/thomas-bsn/forgeyard/internal/agentpb"
	"github.com/thomas-bsn/forgeyard/internal/store/db"
)

// Git push: an app whose source is git is deployed by pushing to the SSH gateway,
// `git push ssh://<app>@<forgeyard>:2222/<app>.git main`. The server keeps a bare repository per app and
// runs git's own receive-pack and upload-pack in it; a push to the branch the app deploys makes it run
// that commit. Its node builds the image from the commit's files, with the Dockerfile at their root,
// getting them from the server (nodes.SourceFunc), and the push shows the build until the app is online.

var gitBranchPattern = regexp.MustCompile(`^[A-Za-z0-9._/-]{1,100}$`)

// validGitBranch reports whether a branch name is one git accepts, among the plain ones.
func validGitBranch(b string) bool {
	return gitBranchPattern.MatchString(b) && !strings.Contains(b, "..") && !strings.HasPrefix(b, "/") &&
		!strings.HasSuffix(b, "/") && !strings.HasSuffix(b, ".lock") && !strings.HasPrefix(b, "-") && !strings.Contains(b, "//")
}

// gitImage is the tag of an app's image built from a commit.
func gitImage(name, commit string) string {
	return "forgeyard/" + name + ":" + commit[:12]
}

// repoDir is an app's bare repository.
func (s *Server) repoDir(appID int64) string {
	return filepath.Join(s.repoRoot, strconv.FormatInt(appID, 10)+".git")
}

// gitRemote is the address an app is pushed to, or "" without a gateway.
func (s *Server) gitRemote(c dnsConfig, name, localHost string) string {
	host := s.sshAddress(c, localHost)
	if host == "" {
		return ""
	}
	if s.sshPort != 22 {
		host += ":" + strconv.Itoa(s.sshPort)
	}
	return "ssh://" + name + "@" + host + "/" + name + ".git"
}

// gitCommand recognizes what git runs over SSH: git-receive-pack '<path>' for a push, git-upload-pack
// '<path>' for a clone or a fetch.
func gitCommand(cmd string) (service, path string, ok bool) {
	for _, svc := range []string{"receive-pack", "upload-pack"} {
		for _, prefix := range []string{"git-" + svc + " ", "git " + svc + " "} {
			if rest, found := strings.CutPrefix(cmd, prefix); found {
				return svc, strings.Trim(strings.TrimSpace(rest), `'"`), true
			}
		}
	}
	return "", "", false
}

// gitSession serves a git command on an SSH channel, then ends it with the command's exit status.
func (s *Server) gitSession(ctx context.Context, ch ssh.Channel, userID, appID int64, service, path string) {
	defer ch.Close()
	// A client that hung up (Ctrl-C during the build) stops the following of its deployment, which goes on.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	out := &hangupWriter{w: ch.Stderr(), cancel: cancel}
	code := s.git(ctx, ch, out, userID, appID, service, path)
	ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{code}))
}

// hangupWriter cancels its context once a write fails: the client is gone.
type hangupWriter struct {
	w      io.Writer
	cancel func()
}

func (h *hangupWriter) Write(p []byte) (int, error) {
	n, err := h.w.Write(p)
	if err != nil {
		h.cancel()
	}
	return n, err
}

func (s *Server) git(ctx context.Context, ch ssh.Channel, out io.Writer, userID, appID int64, service, path string) uint32 {
	say := func(format string, args ...any) {
		fmt.Fprintf(out, "Forgeyard : "+format+"\n", args...)
	}
	app, err := s.store.GetApp(ctx, appID)
	if err != nil {
		say("cette app n'existe plus")
		return 1
	}
	if app.GitBranch == "" {
		say("l'app « %s » ne se déploie pas par git push : choisissez la source « Git push » dans sa configuration", app.Name)
		return 1
	}
	if name := strings.TrimSuffix(strings.Trim(path, "/"), ".git"); name != app.Name {
		say("ce dépôt est celui de l'app « %s » : %s", app.Name, strings.TrimPrefix(path, "/"))
		return 1
	}
	if _, err := exec.LookPath("git"); err != nil {
		say("git n'est pas installé sur la machine de Forgeyard")
		return 1
	}
	dir := s.repoDir(app.ID)
	if service == "upload-pack" {
		if _, err := os.Stat(dir); err != nil {
			say("rien n'a encore été poussé dans « %s »", app.Name)
			return 1
		}
		return s.runGit(ctx, ch, "upload-pack", dir)
	}

	if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(s.repoRoot, 0o700); err != nil {
			s.logger.Error("creating the repositories folder failed", "err", err)
			say("erreur interne")
			return 1
		}
		if msg, err := gitOutput(ctx, "", "init", "--bare", "--quiet", "--initial-branch="+app.GitBranch, dir); err != nil {
			s.logger.Error("creating a repository failed", "app", app.Name, "err", err, "output", msg)
			say("erreur interne")
			return 1
		}
	}
	if code := s.runGit(ctx, ch, "receive-pack", dir); code != 0 {
		return code
	}
	// The branch's commit is deployed unless the app runs it already: pushing the same commit again after
	// a failed deployment (no Dockerfile, no port) tries it again.
	commit, _ := gitOutput(ctx, dir, "rev-parse", "--verify", "--quiet", "refs/heads/"+app.GitBranch)
	switch commit {
	case "":
		say("rien à déployer : l'app déploie la branche « %s » (git push <remote> <votre-branche>:%s, ou changez la branche dans sa configuration)", app.GitBranch, app.GitBranch)
		return 0
	case app.GitCommit:
		say("rien à déployer : l'app tourne déjà le dernier commit de la branche « %s » (Redéployer, dans Forgeyard, le reconstruit)", app.GitBranch)
		return 0
	}
	user, err := s.store.GetUserByID(ctx, userID)
	if err != nil {
		say("erreur interne")
		return 1
	}
	return s.deployCommit(ctx, out, user, app, commit)
}

// runGit runs one of git's transfer commands on an SSH channel: its protocol on the standard input and
// output, its messages on the error output.
func (s *Server) runGit(ctx context.Context, ch ssh.Channel, service, dir string) uint32 {
	cmd := exec.CommandContext(ctx, "git", service, dir)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1")
	cmd.Stdout, cmd.Stderr = ch, ch.Stderr()
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return 1
	}
	if err := cmd.Start(); err != nil {
		s.logger.Error("starting git failed", "service", service, "err", err)
		fmt.Fprintln(ch.Stderr(), "Forgeyard : erreur interne")
		return 1
	}
	// Not cmd.Stdin: Wait would wait for the client to end its input, which git does not do before the end.
	go func() {
		io.Copy(stdin, ch)
		stdin.Close()
	}()
	if err := cmd.Wait(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() > 0 {
			return uint32(exit.ExitCode())
		}
		return 1
	}
	return 0
}

// gitOutput runs a git command in a repository and returns its output, trimmed.
func gitOutput(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return strings.TrimSpace(stderr.String()), err
	}
	return strings.TrimSpace(stdout.String()), nil
}

// dockerfileExpose is the first port a Dockerfile's EXPOSE lines declare, 0 without one.
func dockerfileExpose(dockerfile string) int {
	for _, line := range strings.Split(dockerfile, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 || !strings.EqualFold(f[0], "EXPOSE") {
			continue
		}
		port, _, _ := strings.Cut(f[1], "/")
		if p, err := strconv.Atoi(port); err == nil && p > 0 && p < 65536 {
			return p
		}
	}
	return 0
}

// deployCommit makes an app run a commit just pushed, then follows its build and its start, showing them
// on out, until it is online or failed.
func (s *Server) deployCommit(ctx context.Context, out io.Writer, user db.User, app db.App, commit string) uint32 {
	say := func(format string, args ...any) {
		fmt.Fprintf(out, "Forgeyard : "+format+"\n", args...)
	}
	dir := s.repoDir(app.ID)
	owner, err := s.store.GetUserByID(ctx, app.OwnerID)
	if err != nil {
		say("erreur interne")
		return 1
	}
	if owner.AppsSuspended != 0 {
		say("%s : rien n'est déployé", errSuspended)
		return 1
	}
	dockerfile, err := gitOutput(ctx, dir, "cat-file", "blob", commit+":Dockerfile")
	if err != nil {
		say("pas de Dockerfile à la racine du dépôt : Forgeyard construit l'image avec lui, rien n'est déployé")
		return 1
	}
	port := app.Port
	if app.Kind == kindWeb && port == 0 {
		if port = int64(dockerfileExpose(dockerfile)); port == 0 {
			say("le Dockerfile n'a pas de ligne EXPOSE : indiquez dans la configuration de l'app le port sur lequel elle écoute, puis poussez à nouveau")
			return 1
		}
		say("port %d, lu dans l'EXPOSE du Dockerfile", port)
	}
	subject, _ := gitOutput(ctx, dir, "log", "-1", "--format=%s", commit)

	builds, stop := s.watchBuild(app.ID, commit)
	defer stop()
	app, err = s.store.DeployGitCommit(ctx, db.DeployGitCommitParams{
		GitCommit: commit, Image: gitImage(app.Name, commit), Port: port, UpdatedAt: time.Now().Unix(), ID: app.ID,
	})
	if err != nil {
		s.logger.Error("deploying a pushed commit failed", "app", app.Name, "err", err)
		say("erreur interne")
		return 1
	}
	s.mu.Lock()
	delete(s.crashes, app.ID)
	s.mu.Unlock()
	s.appEvent(app.ID, eventInfo, fmt.Sprintf("Push de %s : %s %s « %s »", user.DisplayName, app.GitBranch, commit[:7], subject))
	s.logger.Info("app pushed", "app", app.Name, "commit", commit, "by", user.DisplayName)
	s.push(ctx, app.NodeID)

	nodeName := "?"
	if node, err := s.store.GetNode(ctx, app.NodeID); err == nil {
		nodeName = node.Name
	}
	say("%s « %s » : construction sur le node %s…", commit[:7], subject, nodeName)

	// The build: its output as it comes, until its result.
	started := false
	startWait := time.NewTimer(time.Minute)
	defer startWait.Stop()
	buildLimit := time.NewTimer(30 * time.Minute)
	defer buildLimit.Stop()
	var built time.Time
	for built.IsZero() {
		select {
		case <-ctx.Done():
			return 1
		case e := <-builds:
			started = true
			if e.Text != "" {
				io.WriteString(out, e.Text)
				continue
			}
			if e.Err != "" {
				msg, _, _ := strings.Cut(e.Err, "\n")
				say("la construction a échoué : %s", msg)
				return 1
			}
			built = time.Now()
		case <-startWait.C:
			if started {
				continue
			}
			if _, online := s.nodes.Live(app.NodeID); !online {
				say("le node %s est hors ligne : l'app sera construite à son retour", nodeName)
				return 0
			}
			say("la construction n'a pas commencé : suivez l'app dans Forgeyard")
			return 0
		case <-buildLimit.C:
			say("toujours en construction après 30 minutes : suivez l'app dans Forgeyard")
			return 0
		}
	}

	// The start: the node reports the new version running, or why it is not.
	say("image construite, démarrage…")
	deadline := time.Now().Add(3 * time.Minute)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return 1
		case <-time.After(time.Second):
		}
		st, ok := s.nodes.AppStatus(app.ID)
		if !ok || st.ReportedAt.Before(built.Add(time.Second)) {
			continue
		}
		switch state := st.Status.GetState(); {
		case state == "running" && st.Status.GetError() == "" && st.Status.GetStartedAt() >= built.Unix()-1:
			c, err := s.loadDNSConfig(ctx)
			if host := webHost(c, app); err == nil && host != "" {
				say("en ligne : https://%s", host)
			} else {
				say("en ligne")
			}
			return 0
		case state == "running" && st.Status.GetError() != "", state == "error", state == "exited":
			msg := st.Status.GetError()
			if msg == "" {
				msg = "elle s'est arrêtée, code " + strconv.Itoa(int(st.Status.GetExitCode())) + " (voir ses logs)"
			}
			say("le déploiement a échoué : %s", msg)
			return 1
		}
	}
	say("pas encore en ligne après 3 minutes : suivez l'app dans Forgeyard")
	return 0
}

// buildEvent is a piece of a build's output, or its result once Done.
type buildEvent struct {
	Text string
	Done bool
	Err  string
}

func buildKey(appID int64, commit string) string {
	return strconv.FormatInt(appID, 10) + "/" + commit
}

// watchBuild follows the builds of an app at a commit, until stop is called.
func (s *Server) watchBuild(appID int64, commit string) (<-chan buildEvent, func()) {
	ch := make(chan buildEvent, 256)
	key := buildKey(appID, commit)
	s.buildMu.Lock()
	s.buildWatchers[key] = append(s.buildWatchers[key], ch)
	s.buildMu.Unlock()
	return ch, func() {
		s.buildMu.Lock()
		defer s.buildMu.Unlock()
		list := s.buildWatchers[key]
		for i, c := range list {
			if c == ch {
				list = append(list[:i], list[i+1:]...)
				break
			}
		}
		if len(list) == 0 {
			delete(s.buildWatchers, key)
		} else {
			s.buildWatchers[key] = list
		}
	}
}

// publishBuild passes a build's event to those following it. Output a slow follower has no room for is
// dropped; the result is not.
func (s *Server) publishBuild(appID int64, commit string, e buildEvent) {
	s.buildMu.Lock()
	list := append([]chan buildEvent(nil), s.buildWatchers[buildKey(appID, commit)]...)
	s.buildMu.Unlock()
	for _, ch := range list {
		if e.Done {
			select {
			case ch <- e:
			case <-time.After(time.Second):
			}
			continue
		}
		select {
		case ch <- e:
		default:
		}
	}
}

// serveSource sends a node building an app the files of its repository at a commit (see nodes.SourceFunc).
func (s *Server) serveSource(ctx context.Context, nodeID, appID int64, commit string, stream agentpb.AgentService_TunnelServer) error {
	a, err := s.store.GetApp(ctx, appID)
	if err != nil {
		return status.Error(codes.NotFound, "app inconnue")
	}
	if a.NodeID != nodeID && a.MovingFrom != nodeID {
		return status.Error(codes.PermissionDenied, "l'app ne tourne pas sur ce node")
	}
	if a.GitCommit == "" || a.GitCommit != commit {
		return status.Error(codes.FailedPrecondition, "ce n'est plus la version de l'app")
	}

	// The build's output, until the agent's result.
	result := make(chan buildEvent, 1)
	go func() {
		for {
			chunk, err := stream.Recv()
			if err != nil {
				result <- buildEvent{Done: true, Err: "le node a coupé la construction"}
				return
			}
			if chunk.GetEof() {
				result <- buildEvent{Done: true, Err: string(chunk.GetData())}
				return
			}
			s.publishBuild(appID, commit, buildEvent{Text: string(chunk.GetData())})
		}
	}()

	cmd := exec.CommandContext(ctx, "git", "archive", "--format=tar", commit)
	cmd.Dir = s.repoDir(appID)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	files, err := cmd.StdoutPipe()
	if err == nil {
		err = cmd.Start()
	}
	if err == nil {
		buf := make([]byte, 32<<10)
		for {
			n, rerr := files.Read(buf)
			if n > 0 {
				if err = stream.Send(&agentpb.TunnelChunk{Data: append([]byte(nil), buf[:n]...)}); err != nil {
					break
				}
			}
			if rerr != nil {
				break
			}
		}
		if werr := cmd.Wait(); err == nil && werr != nil {
			err = fmt.Errorf("%v: %s", werr, strings.TrimSpace(stderr.String()))
		}
	}
	if err == nil {
		err = stream.Send(&agentpb.TunnelChunk{Eof: true})
	}
	if err != nil {
		s.logger.Warn("sending a repository to a node failed", "app", a.Name, "commit", commit, "err", err)
		s.publishBuild(appID, commit, buildEvent{Done: true, Err: "envoi des fichiers au node : " + err.Error()})
		return status.Error(codes.Internal, "envoi des fichiers : "+err.Error())
	}
	select {
	case e := <-result:
		s.publishBuild(appID, commit, e)
		if e.Err == "" {
			s.appEvent(appID, eventInfo, "Image construite : "+commit[:7])
		} else {
			msg, _, _ := strings.Cut(e.Err, "\n")
			s.appEvent(appID, eventError, "Construction de "+commit[:7]+" échouée : "+msg)
		}
	case <-ctx.Done():
	}
	return nil
}

// dropRepo deletes a deleted app's repository.
func (s *Server) dropRepo(appID int64) {
	if s.repoRoot == "" {
		return
	}
	if err := os.RemoveAll(s.repoDir(appID)); err != nil {
		s.logger.Warn("deleting a deleted app's repository failed", "app_id", appID, "err", err)
	}
}
