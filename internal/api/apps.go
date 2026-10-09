package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/thomas-bsn/forgeyard/internal/agentpb"
	"github.com/thomas-bsn/forgeyard/internal/dns"
	"github.com/thomas-bsn/forgeyard/internal/nodes"
	"github.com/thomas-bsn/forgeyard/internal/store/db"
)

const settingAppEnvLabel = "app_env"

var (
	appNamePattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,30}[a-z0-9])?$`)
	envKeyPattern  = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

// appInput is what users set when creating or editing an app.
type appInput struct {
	Name     string            `json:"name"`
	Image    string            `json:"image"`
	Port     int               `json:"port"`
	Env      map[string]string `json:"env"`
	MemoryMB int               `json:"memoryMb"`
}

func (in *appInput) validate(creating bool) string {
	in.Name = strings.ToLower(strings.TrimSpace(in.Name))
	in.Image = strings.TrimSpace(in.Image)
	if creating && !appNamePattern.MatchString(in.Name) {
		return "nom : 1 à 32 caractères parmi a-z, 0-9 et -, sans tiret au début ni à la fin (il devient le sous-domaine)"
	}
	if in.Image == "" || len(in.Image) > 255 || strings.ContainsAny(in.Image, " \t\n") {
		return "image invalide : par exemple nginx:latest ou ghcr.io/moi/mon-app:main"
	}
	if in.Port < 1 || in.Port > 65535 {
		return "port invalide : le port sur lequel l'app écoute dans son conteneur, par exemple 80 ou 3000"
	}
	if in.MemoryMB == 0 {
		in.MemoryMB = 512
	}
	if in.MemoryMB < 64 || in.MemoryMB > 16384 {
		return "mémoire : entre 64 et 16384 Mo"
	}
	if len(in.Env) > 100 {
		return "100 variables d'environnement au maximum"
	}
	for k, v := range in.Env {
		if !envKeyPattern.MatchString(k) {
			return "nom de variable invalide : " + k
		}
		if len(v) > 4096 {
			return "valeur trop longue pour la variable " + k
		}
	}
	return ""
}

// reservedNames cannot be app names: they would clash with Forgeyard's own address or common hosts.
func (s *Server) reservedNames(ctx context.Context, r *http.Request, c dnsConfig) map[string]bool {
	reserved := map[string]bool{"www": true, "forgeyard": true, "mail": true}
	if base, err := s.publicURL(ctx, r); err == nil {
		if u, err := url.Parse(base); err == nil && c.Domain != "" {
			if label, ok := strings.CutSuffix(u.Hostname(), "."+c.Domain); ok && !strings.Contains(label, ".") {
				reserved[label] = true
			}
		}
	}
	return reserved
}

func appHostname(c dnsConfig, name string) string {
	if c.Mode == "none" || c.Domain == "" {
		return ""
	}
	return name + "." + c.Domain
}

// nodeIP is where a node's app domains point: its own public IP, or the instance-wide one.
func nodeIP(node db.Node, c dnsConfig) string {
	if node.PublicIp != "" {
		return node.PublicIp
	}
	return c.PublicIP
}

func (s *Server) sealEnv(env map[string]string) (string, error) {
	if len(env) == 0 {
		return "", nil
	}
	raw, _ := json.Marshal(env)
	return s.secrets.Encrypt(string(raw), settingAppEnvLabel)
}

func (s *Server) openEnv(sealed string) (map[string]string, error) {
	env := map[string]string{}
	if sealed == "" {
		return env, nil
	}
	plain, err := s.secrets.Decrypt(sealed, settingAppEnvLabel)
	if err != nil {
		return nil, err
	}
	return env, json.Unmarshal([]byte(plain), &env)
}

// desiredState is what must run on a node: sent to its agent on connect and after every change.
func (s *Server) desiredState(ctx context.Context, nodeID int64) (*agentpb.DesiredState, error) {
	node, err := s.store.GetNode(ctx, nodeID)
	if err != nil {
		return nil, err
	}
	c, err := s.loadDNSConfig(ctx)
	if err != nil {
		return nil, err
	}
	apps, err := s.store.ListAppsByNode(ctx, nodeID)
	if err != nil {
		return nil, err
	}
	d := &agentpb.DesiredState{Ingress: &agentpb.Ingress{Mode: node.IngressMode, HttpPort: int32(node.IngressHttpPort)}}
	for _, a := range apps {
		env, err := s.openEnv(a.EnvSealed)
		if err != nil {
			return nil, fmt.Errorf("app %s: %w", a.Name, err)
		}
		d.Apps = append(d.Apps, &agentpb.AppSpec{
			Id: a.ID, Name: a.Name, Image: a.Image, Port: int32(a.Port), Env: env,
			Hostname: appHostname(c, a.Name), Running: a.Running != 0,
			MemoryBytes: a.MemoryMb << 20, Generation: a.Generation,
		})
	}
	return d, nil
}

func (s *Server) push(ctx context.Context, nodeID int64) {
	if err := s.nodes.Push(ctx, nodeID); err != nil {
		s.logger.Error("sending the desired state failed", "node_id", nodeID, "err", err)
	}
}

// pickNode chooses where a new app runs: the online node with the fewest apps.
func (s *Server) pickNode(ctx context.Context) (db.Node, error) {
	list, err := s.store.ListNodes(ctx)
	if err != nil {
		return db.Node{}, err
	}
	counts, err := s.store.CountAppsByNode(ctx)
	if err != nil {
		return db.Node{}, err
	}
	perNode := map[int64]int64{}
	for _, c := range counts {
		perNode[c.NodeID] = c.Count
	}
	var best *db.Node
	for i, n := range list {
		if _, online := s.nodes.Live(n.ID); !online || n.Status != "active" {
			continue
		}
		if best == nil || perNode[n.ID] < perNode[best.ID] {
			best = &list[i]
		}
	}
	if best == nil {
		return db.Node{}, errNoNode
	}
	return *best, nil
}

var errNoNode = errors.New("no online node")

// dnsProvider returns the configured DNS provider, or nil when records are not managed by Forgeyard.
func (s *Server) dnsProvider(c dnsConfig) (dns.Provider, error) {
	if c.Mode != "provider" {
		return nil, nil
	}
	return s.newDNSProvider(c.Provider, c.Creds)
}

func (s *Server) setAppRecord(ctx context.Context, c dnsConfig, p dns.Provider, name, ip string) error {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return fmt.Errorf("IP publique du node invalide (%q) : renseignez-la dans Nodes ou dans Réglages", ip)
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	return dns.SetAddress(ctx, p, c.Zone, name, addr)
}

type appResponse struct {
	ID           int64             `json:"id"`
	Name         string            `json:"name"`
	OwnerID      int64             `json:"ownerId"`
	OwnerName    string            `json:"ownerName"`
	NodeID       int64             `json:"nodeId"`
	NodeName     string            `json:"nodeName"`
	Image        string            `json:"image"`
	Port         int64             `json:"port"`
	MemoryMB     int64             `json:"memoryMb"`
	Running      bool              `json:"running"`
	URL          string            `json:"url,omitempty"`
	State        string            `json:"state"`
	Error        string            `json:"error,omitempty"`
	ExitCode     int32             `json:"exitCode,omitempty"`
	OOMKilled    bool              `json:"oomKilled,omitempty"`
	RestartCount int32             `json:"restartCount,omitempty"`
	StartedAt    int64             `json:"startedAt,omitempty"`
	UpdatedAt    int64             `json:"updatedAt"`
	Env          map[string]string `json:"env,omitempty"`
}

func (s *Server) toAppResponse(a db.App, ownerName, nodeName string, c dnsConfig, node *db.Node) appResponse {
	resp := appResponse{
		ID: a.ID, Name: a.Name, OwnerID: a.OwnerID, OwnerName: ownerName, NodeID: a.NodeID, NodeName: nodeName,
		Image: a.Image, Port: a.Port, MemoryMB: a.MemoryMb, Running: a.Running != 0, UpdatedAt: a.UpdatedAt,
		State: "pending",
	}
	if host := appHostname(c, a.Name); host != "" {
		resp.URL = "https://" + host
	}
	if _, online := s.nodes.Live(a.NodeID); !online {
		resp.State = "node-offline"
	}
	if st, ok := s.nodes.AppStatus(a.ID); ok {
		if resp.State != "node-offline" {
			resp.State = st.Status.GetState()
		}
		resp.Error, resp.ExitCode, resp.OOMKilled = st.Status.GetError(), st.Status.GetExitCode(), st.Status.GetOomKilled()
		resp.RestartCount, resp.StartedAt = st.Status.GetRestartCount(), st.Status.GetStartedAt()
		if resp.URL == "" && st.Status.GetHostPort() > 0 && node != nil {
			if ip := nodeIP(*node, c); ip != "" {
				resp.URL = "http://" + ip + ":" + strconv.Itoa(int(st.Status.GetHostPort()))
			}
		}
	}
	return resp
}

func canManage(u db.User, a db.App) bool {
	return isAdmin(u) || a.OwnerID == u.ID
}

func (s *Server) handleListApps(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := currentUser(r)
	var rows []db.ListAppsRow
	var err error
	if isAdmin(user) {
		rows, err = s.store.ListApps(ctx)
	} else {
		var own []db.ListAppsByOwnerRow
		own, err = s.store.ListAppsByOwner(ctx, user.ID)
		for _, o := range own {
			rows = append(rows, db.ListAppsRow(o))
		}
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	c, err := s.loadDNSConfig(ctx)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	nodeList, err := s.store.ListNodes(ctx)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	byID := map[int64]*db.Node{}
	for i := range nodeList {
		byID[nodeList[i].ID] = &nodeList[i]
	}
	out := make([]appResponse, 0, len(rows))
	for _, row := range rows {
		a := db.App{ID: row.ID, Name: row.Name, OwnerID: row.OwnerID, NodeID: row.NodeID, Image: row.Image,
			Port: row.Port, EnvSealed: row.EnvSealed, Running: row.Running, MemoryMb: row.MemoryMb,
			Generation: row.Generation, DnsName: row.DnsName, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
		out = append(out, s.toAppResponse(a, row.OwnerName, row.NodeName, c, byID[row.NodeID]))
	}
	writeJSON(w, http.StatusOK, out)
}

// appFromPath loads the app named in the path, if the current user may manage it.
func (s *Server) appFromPath(w http.ResponseWriter, r *http.Request) (db.App, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusNotFound, "app introuvable")
		return db.App{}, false
	}
	a, err := s.store.GetApp(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !canManage(currentUser(r), a)) {
		writeError(w, http.StatusNotFound, "app introuvable")
		return db.App{}, false
	}
	if err != nil {
		s.internalError(w, r, err)
		return db.App{}, false
	}
	return a, true
}

func (s *Server) writeApp(w http.ResponseWriter, r *http.Request, status int, a db.App, withEnv bool) {
	ctx := r.Context()
	c, err := s.loadDNSConfig(ctx)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	owner, err := s.store.GetUserByID(ctx, a.OwnerID)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	node, err := s.store.GetNode(ctx, a.NodeID)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	resp := s.toAppResponse(a, owner.DisplayName, node.Name, c, &node)
	if withEnv {
		if resp.Env, err = s.openEnv(a.EnvSealed); err != nil {
			s.internalError(w, r, err)
			return
		}
	}
	writeJSON(w, status, resp)
}

func (s *Server) handleGetApp(w http.ResponseWriter, r *http.Request) {
	if a, ok := s.appFromPath(w, r); ok {
		s.writeApp(w, r, http.StatusOK, a, true)
	}
}

func (s *Server) handleCreateApp(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var in appInput
	if !decodeJSON(w, r, &in) {
		return
	}
	if msg := in.validate(true); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	c, err := s.loadDNSConfig(ctx)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if s.reservedNames(ctx, r, c)[in.Name] {
		writeError(w, http.StatusConflict, "ce nom est réservé, choisissez-en un autre")
		return
	}
	node, err := s.pickNode(ctx)
	if errors.Is(err, errNoNode) {
		writeError(w, http.StatusServiceUnavailable, "aucun node en ligne pour accueillir l'app")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	sealed, err := s.sealEnv(in.Env)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	now := time.Now().Unix()
	app, err := s.store.CreateApp(ctx, db.CreateAppParams{
		Name: in.Name, OwnerID: currentUser(r).ID, NodeID: node.ID, Image: in.Image, Port: int64(in.Port),
		EnvSealed: sealed, MemoryMb: int64(in.MemoryMB), CreatedAt: now, UpdatedAt: now,
	})
	if err != nil && strings.Contains(err.Error(), "UNIQUE") {
		writeError(w, http.StatusConflict, "une app porte déjà ce nom")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}

	// The DNS record comes before the deployment, so the domain resolves by the time the container is up.
	if p, err := s.dnsProvider(c); err != nil {
		s.failCreate(w, r, app, "DNS : "+err.Error())
		return
	} else if p != nil {
		host := appHostname(c, app.Name)
		if err := s.setAppRecord(ctx, c, p, host, nodeIP(node, c)); err != nil {
			s.failCreate(w, r, app, "création de l'enregistrement DNS "+host+" : "+err.Error())
			return
		}
		if err := s.store.SetAppDNSName(ctx, db.SetAppDNSNameParams{DnsName: host, ID: app.ID}); err != nil {
			s.internalError(w, r, err)
			return
		}
	}

	s.logger.Info("app created", "app", app.Name, "image", app.Image, "node", node.Name, "by", currentUser(r).DisplayName)
	s.push(ctx, node.ID)
	s.writeApp(w, r, http.StatusCreated, app, true)
}

func (s *Server) failCreate(w http.ResponseWriter, r *http.Request, app db.App, msg string) {
	if err := s.store.DeleteApp(r.Context(), app.ID); err != nil {
		s.logger.Error("rolling back app creation failed", "app", app.Name, "err", err)
	}
	writeError(w, http.StatusBadGateway, msg)
}

func (s *Server) handleUpdateApp(w http.ResponseWriter, r *http.Request) {
	a, ok := s.appFromPath(w, r)
	if !ok {
		return
	}
	var in appInput
	if !decodeJSON(w, r, &in) {
		return
	}
	if msg := in.validate(false); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	sealed, err := s.sealEnv(in.Env)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	a, err = s.store.UpdateAppConfig(r.Context(), db.UpdateAppConfigParams{
		Image: in.Image, Port: int64(in.Port), EnvSealed: sealed, MemoryMb: int64(in.MemoryMB),
		UpdatedAt: time.Now().Unix(), ID: a.ID,
	})
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.logger.Info("app updated", "app", a.Name, "image", a.Image, "by", currentUser(r).DisplayName)
	s.push(r.Context(), a.NodeID)
	s.writeApp(w, r, http.StatusOK, a, true)
}

// handleAppAction starts, stops or redeploys an app. Starting and redeploying create a fresh container
// (and pull the image again), which also gets a crashed app going.
func (s *Server) handleAppAction(w http.ResponseWriter, r *http.Request) {
	a, ok := s.appFromPath(w, r)
	if !ok {
		return
	}
	params := db.SetAppRunningParams{UpdatedAt: time.Now().Unix(), ID: a.ID}
	switch r.PathValue("action") {
	case "start", "redeploy":
		params.Running, params.Generation = 1, 1
	case "stop":
		params.Running, params.Generation = 0, 0
	default:
		writeError(w, http.StatusNotFound, "action inconnue")
		return
	}
	a, err := s.store.SetAppRunning(r.Context(), params)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.logger.Info("app "+r.PathValue("action"), "app", a.Name, "by", currentUser(r).DisplayName)
	s.push(r.Context(), a.NodeID)
	s.writeApp(w, r, http.StatusOK, a, false)
}

func (s *Server) handleDeleteApp(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	a, ok := s.appFromPath(w, r)
	if !ok {
		return
	}
	if a.DnsName != "" {
		c, err := s.loadDNSConfig(ctx)
		if err == nil {
			var p dns.Provider
			if p, err = s.dnsProvider(c); err == nil && p != nil {
				dctx, cancel := context.WithTimeout(ctx, 20*time.Second)
				err = dns.DeleteAddress(dctx, p, c.Zone, a.DnsName, netip.Addr{})
				cancel()
			}
		}
		// The app goes anyway: a leftover record is harmless and can be removed by hand.
		if err != nil {
			s.logger.Warn("deleting the app's DNS record failed", "app", a.Name, "record", a.DnsName, "err", err)
		}
	}
	if err := s.store.DeleteApp(ctx, a.ID); err != nil {
		s.internalError(w, r, err)
		return
	}
	s.nodes.ForgetApp(a.ID)
	s.logger.Info("app deleted", "app", a.Name, "by", currentUser(r).DisplayName)
	s.push(ctx, a.NodeID)
	w.WriteHeader(http.StatusNoContent)
}

// handleAppLogs streams an app's output as server-sent events, starting with the last lines.
func (s *Server) handleAppLogs(w http.ResponseWriter, r *http.Request) {
	a, ok := s.appFromPath(w, r)
	if !ok {
		return
	}
	tail, _ := strconv.Atoi(r.URL.Query().Get("tail"))
	if tail <= 0 || tail > 2000 {
		tail = 200
	}
	lines, err := s.nodes.Logs(r.Context(), a.NodeID, a.ID, tail)
	if errors.Is(err, nodes.ErrOffline) {
		writeError(w, http.StatusServiceUnavailable, "le node de cette app est hors ligne")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	flusher, _ := w.(http.Flusher)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	if flusher != nil {
		flusher.Flush()
	}
	type event struct {
		Text   string `json:"text"`
		Stderr bool   `json:"stderr,omitempty"`
		End    bool   `json:"end,omitempty"`
	}
	for line := range lines {
		raw, _ := json.Marshal(event{Text: line.GetText(), Stderr: line.GetStderr(), End: line.GetEnd()})
		fmt.Fprintf(w, "data: %s\n\n", raw)
		if flusher != nil {
			flusher.Flush()
		}
	}
}
