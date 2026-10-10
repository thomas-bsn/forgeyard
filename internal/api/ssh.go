package api

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"encoding/binary"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/ssh"

	"github.com/thomas-bsn/forgeyard/internal/nodes"
	"github.com/thomas-bsn/forgeyard/internal/store/db"
)

// The SSH gateway: `ssh <app>@<forgeyard> -p 2222` opens a shell in the app's container, wherever its node
// is, with no SSH server in the container and a single port to open: the connection ends here and goes on
// over the agent's stream, like the web terminal. People sign in with the public keys of their profile;
// the user name is the app's, which they must own (or be an admin).

// LoadSSHHostKey reads the gateway's host key, creating it on first start: it must stay the same, or
// clients warn that the host changed.
func LoadSSHHostKey(path string) (ssh.Signer, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		_, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return nil, err
		}
		block, err := ssh.MarshalPrivateKey(priv, "forgeyard ssh gateway")
		if err != nil {
			return nil, err
		}
		raw = pem.EncodeToMemory(block)
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	return ssh.ParsePrivateKey(raw)
}

// sshCommand is how an app's owner reaches it, or "" without a gateway.
func (s *Server) sshCommand(c dnsConfig, name, localHost string) string {
	if s.sshPort == 0 {
		return ""
	}
	host := s.sshHost
	if host == "" {
		host = c.PublicIP
	}
	if host == "" {
		host = localHost
	}
	if host == "" {
		return ""
	}
	cmd := "ssh " + name + "@" + host
	if s.sshPort != 22 {
		cmd += " -p " + strconv.Itoa(s.sshPort)
	}
	return cmd
}

// ServeSSH runs the gateway on l until ctx ends.
func (s *Server) ServeSSH(ctx context.Context, l net.Listener, hostKey ssh.Signer) {
	go func() {
		<-ctx.Done()
		l.Close()
	}()
	for {
		nc, err := l.Accept()
		if err != nil {
			if ctx.Err() == nil {
				s.logger.Warn("ssh gateway stopped accepting", "err", err)
			}
			return
		}
		go s.sshConn(ctx, nc, hostKey)
	}
}

func (s *Server) sshConn(ctx context.Context, nc net.Conn, hostKey ssh.Signer) {
	defer nc.Close()
	ip, _, _ := net.SplitHostPort(nc.RemoteAddr().String())
	if !s.limiter.Allowed("ssh:" + ip) {
		return
	}
	cfg := &ssh.ServerConfig{
		ServerVersion: "SSH-2.0-Forgeyard",
		PublicKeyCallback: func(meta ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			return s.sshAuth(ctx, meta.User(), key)
		},
	}
	cfg.AddHostKey(hostKey)
	nc.SetDeadline(time.Now().Add(30 * time.Second)) // the handshake must not hang
	conn, chans, reqs, err := ssh.NewServerConn(nc, cfg)
	if err != nil {
		// Clients offer their keys one by one: a connection that signs in with none counts once.
		s.limiter.Fail("ssh:" + ip)
		return
	}
	nc.SetDeadline(time.Time{})
	defer conn.Close()
	go ssh.DiscardRequests(reqs)

	userID, _ := strconv.ParseInt(conn.Permissions.Extensions["user"], 10, 64)
	appID, _ := strconv.ParseInt(conn.Permissions.Extensions["app"], 10, 64)
	keyID, _ := strconv.ParseInt(conn.Permissions.Extensions["key"], 10, 64)
	s.store.TouchSSHKey(ctx, db.TouchSSHKeyParams{LastUsedAt: time.Now().Unix(), ID: keyID})
	for nch := range chans {
		if nch.ChannelType() != "session" {
			nch.Reject(ssh.UnknownChannelType, "seules les sessions shell sont acceptées")
			continue
		}
		ch, chReqs, err := nch.Accept()
		if err != nil {
			continue
		}
		go s.sshSession(ctx, ch, chReqs, userID, appID, ip)
	}
}

// sshAuth accepts a key of the account owning the app named by the SSH user name, or of an admin.
func (s *Server) sshAuth(ctx context.Context, appName string, key ssh.PublicKey) (*ssh.Permissions, error) {
	refused := errors.New("clé ou app inconnue")
	k, err := s.store.GetSSHKeyByFingerprint(ctx, ssh.FingerprintSHA256(key))
	if err != nil {
		return nil, refused
	}
	known, _, _, _, err := ssh.ParseAuthorizedKey([]byte(k.PublicKey))
	if err != nil || string(known.Marshal()) != string(key.Marshal()) {
		return nil, refused
	}
	user, err := s.store.GetUserByID(ctx, k.UserID)
	if err != nil || user.Disabled != 0 {
		return nil, refused
	}
	app, err := s.store.GetAppByName(ctx, strings.ToLower(appName))
	if err != nil || !canManage(user, app) {
		return nil, refused
	}
	return &ssh.Permissions{Extensions: map[string]string{
		"user": strconv.FormatInt(user.ID, 10), "app": strconv.FormatInt(app.ID, 10), "key": strconv.FormatInt(k.ID, 10),
	}}, nil
}

// sshSession serves one session: a shell (with or without a terminal) or a command, in the app's container.
func (s *Server) sshSession(ctx context.Context, ch ssh.Channel, reqs <-chan *ssh.Request, userID, appID int64, ip string) {
	defer ch.Close()
	cols, rows := uint32(80), uint32(24)
	var sess *nodes.ExecSession
	fail := func(msg string) {
		fmt.Fprintf(ch.Stderr(), "Forgeyard : %s\r\n", msg)
		ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{1}))
	}
	for req := range reqs {
		switch req.Type {
		case "pty-req":
			// string term, uint32 cols, uint32 rows, …
			if c, r, ok := ptySize(req.Payload); ok {
				cols, rows = c, r
			}
			req.Reply(true, nil)
		case "window-change":
			if len(req.Payload) >= 8 && sess != nil {
				sess.Resize(binary.BigEndian.Uint32(req.Payload), binary.BigEndian.Uint32(req.Payload[4:]))
			}
		case "env":
			req.Reply(true, nil) // accepted and ignored: the container has its own environment
		case "shell", "exec":
			if sess != nil {
				req.Reply(false, nil)
				continue
			}
			command := ""
			if req.Type == "exec" {
				var p struct{ Command string }
				if ssh.Unmarshal(req.Payload, &p) != nil {
					req.Reply(false, nil)
					continue
				}
				command = p.Command
			}
			req.Reply(true, nil)
			app, err := s.store.GetApp(ctx, appID)
			if errors.Is(err, sql.ErrNoRows) {
				fail("cette app n'existe plus")
				return
			}
			if err != nil {
				fail("erreur interne")
				return
			}
			if st, ok := s.nodes.AppStatus(app.ID); !ok || st.Status.GetState() != "running" {
				fail("l'app « " + app.Name + " » n'est pas en ligne : démarrez-la depuis Forgeyard")
				return
			}
			sess, err = s.nodes.Exec(app.NodeID, app.ID, "", command, cols, rows)
			if errors.Is(err, nodes.ErrOffline) {
				fail("le node de cette app est hors ligne")
				return
			}
			if err != nil {
				fail(err.Error())
				return
			}
			defer sess.Close()
			name := "?"
			if u, err := s.store.GetUserByID(ctx, userID); err == nil {
				name = u.DisplayName
			}
			s.appEvent(app.ID, eventInfo, "Connexion SSH de "+name)
			s.logger.Info("ssh session", "app", app.Name, "by", name, "ip", ip, "command", command != "")
			go func() {
				buf := make([]byte, 32<<10)
				for {
					n, err := ch.Read(buf)
					if n > 0 {
						sess.Input(append([]byte(nil), buf[:n]...))
					}
					if err != nil {
						if err == io.EOF && command != "" {
							sess.Input([]byte{4}) // end of input for a command reading it
						}
						return
					}
				}
			}()
			go func() {
				defer ch.Close()
				for out := range sess.Output {
					if len(out.GetData()) > 0 {
						if _, err := ch.Write(out.GetData()); err != nil {
							return
						}
					}
					if out.GetClosed() {
						if msg := out.GetError(); msg != "" {
							fmt.Fprintf(ch.Stderr(), "Forgeyard : %s\r\n", msg)
						}
						code := out.GetExitCode()
						if out.GetError() != "" && code == 0 {
							code = 1
						}
						ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{uint32(code)}))
						return
					}
				}
			}()
		default:
			if req.WantReply {
				req.Reply(false, nil) // sftp, port forwarding…: not offered
			}
		}
	}
}

// ptySize reads the terminal size of a pty-req: string term, uint32 columns, uint32 rows.
func ptySize(p []byte) (uint32, uint32, bool) {
	if len(p) < 4 {
		return 0, 0, false
	}
	n := binary.BigEndian.Uint32(p)
	if uint64(len(p)) < 4+uint64(n)+8 {
		return 0, 0, false
	}
	p = p[4+n:]
	c, r := binary.BigEndian.Uint32(p), binary.BigEndian.Uint32(p[4:])
	if c == 0 || r == 0 || c > 1000 || r > 500 {
		return 0, 0, false
	}
	return c, r, true
}

// --- Keys, in Profile › Clés SSH ---

type sshKeyResponse struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Type        string `json:"type"`
	Fingerprint string `json:"fingerprint"`
	CreatedAt   int64  `json:"createdAt"`
	LastUsedAt  int64  `json:"lastUsedAt,omitempty"`
}

func toSSHKeyResponse(k db.SshKey) sshKeyResponse {
	typ := ""
	if pk, _, _, _, err := ssh.ParseAuthorizedKey([]byte(k.PublicKey)); err == nil {
		typ = pk.Type()
	}
	return sshKeyResponse{ID: k.ID, Name: k.Name, Type: typ, Fingerprint: k.Fingerprint, CreatedAt: k.CreatedAt, LastUsedAt: k.LastUsedAt}
}

func (s *Server) handleListSSHKeys(w http.ResponseWriter, r *http.Request) {
	keys, err := s.store.ListSSHKeys(r.Context(), currentUser(r).ID)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	out := make([]sshKeyResponse, 0, len(keys))
	for _, k := range keys {
		out = append(out, toSSHKeyResponse(k))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleAddSSHKey(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name      string `json:"name"`
		PublicKey string `json:"publicKey"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	pk, comment, _, _, err := ssh.ParseAuthorizedKey([]byte(strings.TrimSpace(in.PublicKey)))
	if err != nil {
		writeError(w, http.StatusBadRequest, "clé publique illisible : collez le contenu de ~/.ssh/id_ed25519.pub (une ligne qui commence par ssh-ed25519, ssh-rsa ou ecdsa-…)")
		return
	}
	switch pk.Type() {
	case ssh.KeyAlgoED25519, ssh.KeyAlgoECDSA256, ssh.KeyAlgoECDSA384, ssh.KeyAlgoECDSA521, ssh.KeyAlgoSKED25519, ssh.KeyAlgoSKECDSA256:
	case ssh.KeyAlgoRSA:
		if ck, ok := pk.(ssh.CryptoPublicKey); ok {
			if rk, ok := ck.CryptoPublicKey().(interface{ Size() int }); ok && rk.Size()*8 < 2048 {
				writeError(w, http.StatusBadRequest, "clé RSA trop courte : 2048 bits au moins, ou mieux une clé ed25519")
				return
			}
		}
	default:
		writeError(w, http.StatusBadRequest, "type de clé non accepté : "+pk.Type())
		return
	}
	name := strings.Join(strings.Fields(in.Name), " ")
	if name == "" {
		name = comment
	}
	if name == "" {
		name = "Ma clé"
	}
	if utf8.RuneCountInString(name) > 60 {
		name = string([]rune(name)[:60])
	}
	k, err := s.store.CreateSSHKey(r.Context(), db.CreateSSHKeyParams{
		UserID: currentUser(r).ID, Name: name, PublicKey: strings.TrimSpace(string(ssh.MarshalAuthorizedKey(pk))),
		Fingerprint: ssh.FingerprintSHA256(pk), CreatedAt: time.Now().Unix(),
	})
	if err != nil && strings.Contains(err.Error(), "UNIQUE") {
		writeError(w, http.StatusConflict, "cette clé est déjà enregistrée (sur ce compte ou un autre)")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.logger.Info("ssh key added", "user", currentUser(r).DisplayName, "fingerprint", k.Fingerprint)
	writeJSON(w, http.StatusCreated, toSSHKeyResponse(k))
}

func (s *Server) handleDeleteSSHKey(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	n, err := s.store.DeleteSSHKey(r.Context(), db.DeleteSSHKeyParams{ID: id, UserID: currentUser(r).ID})
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if n == 0 {
		writeError(w, http.StatusNotFound, "clé introuvable")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
