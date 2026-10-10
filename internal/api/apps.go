package api

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"sort"
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
	Name  string `json:"name"`
	Image string `json:"image"`
	// Dockerfile, when set, is built on the node instead of pulling Image, which Forgeyard then names.
	Dockerfile string            `json:"dockerfile"`
	Port       int               `json:"port"`
	Env        map[string]string `json:"env"`
	MemoryMB   int               `json:"memoryMb"`
	NodeID     int64             `json:"nodeId"` // at creation: where it runs, 0 for the recommended node
}

func (in *appInput) validate(creating bool) string {
	in.Name = strings.ToLower(strings.TrimSpace(in.Name))
	in.Image = strings.TrimSpace(in.Image)
	if creating && !appNamePattern.MatchString(in.Name) {
		return "nom : 1 à 32 caractères parmi a-z, 0-9 et -, sans tiret au début ni à la fin (il devient le sous-domaine)"
	}
	in.Dockerfile = strings.TrimSpace(strings.ReplaceAll(in.Dockerfile, "\r\n", "\n"))
	if in.Dockerfile != "" {
		if len(in.Dockerfile) > 64<<10 {
			return "Dockerfile trop long : 64 Ko au maximum"
		}
		if dockerfileBase(in.Dockerfile) == "" {
			return "Dockerfile invalide : il doit partir d'une image, avec une ligne FROM"
		}
		in.Image = ""
	} else if in.Image == "" || len(in.Image) > 255 || strings.ContainsAny(in.Image, " \t\n") {
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

// dockerfileBase is the image a Dockerfile starts from, its first FROM, or "" without one.
func dockerfileBase(dockerfile string) string {
	for _, line := range strings.Split(dockerfile, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 || !strings.EqualFold(f[0], "FROM") {
			continue
		}
		f = f[1:]
		for len(f) > 1 && strings.HasPrefix(f[0], "--") {
			f = f[1:] // --platform=…
		}
		return f[0]
	}
	return ""
}

// builtImage is the tag of an app's image built from its Dockerfile: a new Dockerfile gives a new tag,
// so the node rolls out the new version next to the running one.
func builtImage(name, dockerfile string) string {
	sum := sha256.Sum256([]byte(dockerfile))
	return "forgeyard/" + name + ":" + hex.EncodeToString(sum[:6])
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
	apps, err := s.store.ListAppsForNode(ctx, nodeID)
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
			MemoryBytes: a.MemoryMb << 20, Generation: a.Generation, Leaving: a.MovingFrom == nodeID,
			Dockerfile: a.Dockerfile,
		})
	}
	if node.IsLocal != 0 {
		if d.Relays, err = s.relays(ctx, node, c); err != nil {
			return nil, err
		}
	}
	return d, nil
}

// relays lists the apps Forgeyard's own machine passes on to other nodes. Behind one public IP (a home
// box), web traffic reaches a single machine: the one running Forgeyard, whose proxy or Traefik receives
// it. Apps on other nodes with the same public IP are relayed there over the local network, to their
// node's Traefik in "behind my proxy" mode.
func (s *Server) relays(ctx context.Context, front db.Node, c dnsConfig) ([]*agentpb.Relay, error) {
	ip := nodeIP(front, c)
	if ip == "" || c.Mode == "none" {
		return nil, nil
	}
	nodeList, err := s.store.ListNodes(ctx)
	if err != nil {
		return nil, err
	}
	var out []*agentpb.Relay
	for _, n := range nodeList {
		if n.ID == front.ID || n.Status != "active" || nodeIP(n, c) != ip || n.LocalIp == "" || n.IngressMode != "proxy" {
			continue
		}
		apps, err := s.store.ListAppsByNode(ctx, n.ID)
		if err != nil {
			return nil, err
		}
		target := "http://" + net.JoinHostPort(n.LocalIp, strconv.FormatInt(n.IngressHttpPort, 10))
		for _, a := range apps {
			if host := appHostname(c, a.Name); host != "" {
				out = append(out, &agentpb.Relay{Hostname: host, Target: target})
			}
		}
	}
	return out, nil
}

// push sends a node its desired state, and Forgeyard's own machine too, whose relays follow the apps of
// the other nodes.
func (s *Server) push(ctx context.Context, nodeID int64) {
	if err := s.nodes.Push(ctx, nodeID); err != nil {
		s.logger.Error("sending the desired state failed", "node_id", nodeID, "err", err)
	}
	s.pushRelays(ctx, nodeID)
}

// pushRelays sends Forgeyard's own machine its desired state again, after a change on another node.
func (s *Server) pushRelays(ctx context.Context, changedNodeID int64) {
	if local, err := s.store.GetLocalNode(ctx); err == nil && local.ID != changedNodeID {
		if err := s.nodes.Push(ctx, local.ID); err != nil {
			s.logger.Error("sending the desired state failed", "node_id", local.ID, "err", err)
		}
	}
}

// nodeChoice is a node an app can be created on, with what it has left.
type nodeChoice struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Cpus        int64  `json:"cpus"`
	MemoryBytes int64  `json:"memoryBytes"`
	FreeMemory  int64  `json:"freeMemoryBytes"`
	Apps        int64  `json:"apps"`
	Recommended bool   `json:"recommended"`
}

// nodeChoices lists the online nodes, the recommended one first: the one with the most free memory, then
// the most CPUs, then the fewest apps. A Raspberry Pi next to a NAS gets the apps only when chosen.
func (s *Server) nodeChoices(ctx context.Context) ([]nodeChoice, error) {
	list, err := s.store.ListNodes(ctx)
	if err != nil {
		return nil, err
	}
	counts, err := s.store.CountAppsByNode(ctx)
	if err != nil {
		return nil, err
	}
	perNode := map[int64]int64{}
	for _, c := range counts {
		perNode[c.NodeID] = c.Count
	}
	var out []nodeChoice
	for _, n := range list {
		live, online := s.nodes.Live(n.ID)
		if !online || n.Status != "active" {
			continue
		}
		free := n.MemoryBytes
		if k := len(live.Metrics); k > 0 {
			free = max(0, n.MemoryBytes-int64(live.Metrics[k-1].GetMemoryUsedBytes()))
		}
		out = append(out, nodeChoice{ID: n.ID, Name: n.Name, Cpus: n.Cpus, MemoryBytes: n.MemoryBytes, FreeMemory: free, Apps: perNode[n.ID]})
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.FreeMemory != b.FreeMemory {
			return a.FreeMemory > b.FreeMemory
		}
		if a.Cpus != b.Cpus {
			return a.Cpus > b.Cpus
		}
		return a.Apps < b.Apps
	})
	if len(out) > 0 {
		out[0].Recommended = true
	}
	return out, nil
}

// pickNode chooses where a new app runs: the chosen node if it is online, else the recommended one.
func (s *Server) pickNode(ctx context.Context, chosen int64) (db.Node, error) {
	choices, err := s.nodeChoices(ctx)
	if err != nil {
		return db.Node{}, err
	}
	if len(choices) == 0 {
		return db.Node{}, errNoNode
	}
	id := choices[0].ID
	if chosen != 0 {
		found := false
		for _, c := range choices {
			if c.ID == chosen {
				found = true
			}
		}
		if !found {
			return db.Node{}, errNodeUnavailable
		}
		id = chosen
	}
	return s.store.GetNode(ctx, id)
}

func (s *Server) handleNodeChoices(w http.ResponseWriter, r *http.Request) {
	choices, err := s.nodeChoices(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if choices == nil {
		choices = []nodeChoice{}
	}
	writeJSON(w, http.StatusOK, choices)
}

var (
	errNoNode          = errors.New("no online node")
	errNodeUnavailable = errors.New("chosen node offline or unknown")
)

// errSuspended is shown when an admin suspended the apps of the app's owner.
const errSuspended = "les apps de ce compte sont suspendues par un admin"

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
	ID             int64             `json:"id"`
	Name           string            `json:"name"`
	OwnerID        int64             `json:"ownerId"`
	OwnerName      string            `json:"ownerName"`
	NodeID         int64             `json:"nodeId"`
	NodeName       string            `json:"nodeName"`
	Image          string            `json:"image"`
	Port           int64             `json:"port"`
	MemoryMB       int64             `json:"memoryMb"`
	Running        bool              `json:"running"`
	URL            string            `json:"url,omitempty"`
	State          string            `json:"state"`
	Error          string            `json:"error,omitempty"`
	ExitCode       int32             `json:"exitCode,omitempty"`
	OOMKilled      bool              `json:"oomKilled,omitempty"`
	RestartCount   int32             `json:"restartCount,omitempty"`
	StartedAt      int64             `json:"startedAt,omitempty"`
	HostPort       int32             `json:"hostPort,omitempty"`
	CPUPercent     float64           `json:"cpuPercent,omitempty"`
	MemoryUsed     uint64            `json:"memoryUsedBytes,omitempty"`
	Suspended      bool              `json:"suspended,omitempty"`
	Public         bool              `json:"public"`
	Logo           appLogo           `json:"logo"`
	CrashSuspended bool              `json:"crashSuspended,omitempty"`
	MovingFrom     int64             `json:"movingFrom,omitempty"` // the node it leaves, while it moves
	UpdatedAt      int64             `json:"updatedAt"`
	Env            map[string]string `json:"env,omitempty"`
	Dockerfile     string            `json:"dockerfile,omitempty"` // with the env, for the app's owner
}

// toAppResponse describes an app. localHost is the host Forgeyard is reached at, used as the address of its
// own machine when that node has no public IP set.
func (s *Server) toAppResponse(a db.App, ownerName string, ownerSuspended bool, nodeName string, c dnsConfig, node *db.Node, localHost string) appResponse {
	resp := appResponse{
		ID: a.ID, Name: a.Name, OwnerID: a.OwnerID, OwnerName: ownerName, NodeID: a.NodeID, NodeName: nodeName,
		Image: a.Image, Port: a.Port, MemoryMB: a.MemoryMb, Running: a.Running != 0, UpdatedAt: a.UpdatedAt,
		State: "pending", Suspended: ownerSuspended, Public: a.Public != 0, Logo: toAppLogo(a), CrashSuspended: a.CrashSuspended != 0, MovingFrom: a.MovingFrom,
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
		resp.HostPort = st.Status.GetHostPort()
		if resp.State == "running" {
			resp.CPUPercent, resp.MemoryUsed = st.Status.GetCpuPercent(), st.Status.GetMemoryUsedBytes()
		}
		if resp.URL == "" && resp.HostPort > 0 && node != nil {
			host := nodeIP(*node, c)
			if host == "" && node.IsLocal != 0 {
				host = localHost
			}
			if host != "" {
				resp.URL = "http://" + net.JoinHostPort(host, strconv.Itoa(int(resp.HostPort)))
			}
		}
	}
	return resp
}

// publicHost is the host name of Forgeyard's address.
func (s *Server) publicHost(ctx context.Context, r *http.Request) string {
	base, err := s.publicURL(ctx, r)
	if err != nil {
		return ""
	}
	u, err := url.Parse(base)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

// syncApps brings every app in line with the current domain settings: their DNS records at the provider and
// the domains their nodes route. It runs after the domain settings change.
func (s *Server) syncApps(ctx context.Context) error {
	c, err := s.loadDNSConfig(ctx)
	if err != nil {
		return err
	}
	provider, err := s.dnsProvider(c)
	if err != nil {
		return err
	}
	nodeList, err := s.store.ListNodes(ctx)
	if err != nil {
		return err
	}
	var failed []string
	for _, node := range nodeList {
		apps, err := s.store.ListAppsByNode(ctx, node.ID)
		if err != nil {
			return err
		}
		for _, a := range apps {
			host := appHostname(c, a.Name)
			if provider != nil && host != "" {
				// A record Forgeyard did not create belongs to another site: leave it alone.
				if host != a.DnsName {
					taken, err := dns.Exists(ctx, provider, c.Zone, host)
					if err != nil {
						failed = append(failed, host+" ("+err.Error()+")")
						continue
					}
					if taken {
						failed = append(failed, host+" (existe déjà pour un autre site : renommez l'app)")
						if a.DnsName != "" {
							if err := s.store.SetAppDNSName(ctx, db.SetAppDNSNameParams{DnsName: "", ID: a.ID}); err != nil {
								return err
							}
						}
						continue
					}
				}
				if err := s.setAppRecord(ctx, c, provider, host, nodeIP(node, c)); err != nil {
					failed = append(failed, host+" ("+err.Error()+")")
					continue
				}
			} else {
				host = "" // records are not managed by Forgeyard
			}
			if host != a.DnsName {
				if err := s.store.SetAppDNSName(ctx, db.SetAppDNSNameParams{DnsName: host, ID: a.ID}); err != nil {
					return err
				}
			}
		}
		s.push(ctx, node.ID)
	}
	if len(failed) > 0 {
		return fmt.Errorf("enregistrements DNS non créés : %s", strings.Join(failed, ", "))
	}
	return nil
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
			Generation: row.Generation, DnsName: row.DnsName, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt, Public: row.Public,
			LogoMode: row.LogoMode, LogoColor: row.LogoColor, LogoUpdatedAt: row.LogoUpdatedAt, CrashSuspended: row.CrashSuspended,
			MovingFrom: row.MovingFrom, MovedAt: row.MovedAt}
		out = append(out, s.toAppResponse(a, row.OwnerName, row.OwnerSuspended != 0, row.NodeName, c, byID[row.NodeID], s.publicHost(ctx, r)))
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
	resp := s.toAppResponse(a, owner.DisplayName, owner.AppsSuspended != 0, node.Name, c, &node, s.publicHost(ctx, r))
	if withEnv {
		if resp.Env, err = s.openEnv(a.EnvSealed); err != nil {
			s.internalError(w, r, err)
			return
		}
		resp.Dockerfile = a.Dockerfile
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
	if currentUser(r).AppsSuspended != 0 {
		writeError(w, http.StatusForbidden, errSuspended)
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
	// A name already in the DNS zone belongs to another site: its record must not be overwritten.
	if p, err := s.dnsProvider(c); err == nil && p != nil {
		dctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		taken, err := dns.Exists(dctx, p, c.Zone, appHostname(c, in.Name))
		cancel()
		if err != nil {
			writeError(w, http.StatusBadGateway, "vérification du DNS : "+err.Error())
			return
		}
		if taken {
			writeError(w, http.StatusConflict, appHostname(c, in.Name)+" existe déjà dans votre DNS (un autre site l'utilise) : choisissez un autre nom")
			return
		}
	}
	// Only admins pick the node; users' apps go to the recommended one.
	if in.NodeID != 0 && !isAdmin(currentUser(r)) {
		writeError(w, http.StatusForbidden, "seul un admin choisit la machine d'une app")
		return
	}
	node, err := s.pickNode(ctx, in.NodeID)
	if errors.Is(err, errNoNode) {
		writeError(w, http.StatusServiceUnavailable, "aucun node en ligne pour accueillir l'app")
		return
	}
	if errors.Is(err, errNodeUnavailable) {
		writeError(w, http.StatusConflict, "cette machine n'est pas en ligne : choisissez-en une autre")
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
	if in.Dockerfile != "" {
		in.Image = builtImage(in.Name, in.Dockerfile)
	}
	now := time.Now().Unix()
	app, err := s.store.CreateApp(ctx, db.CreateAppParams{
		Name: in.Name, OwnerID: currentUser(r).ID, NodeID: node.ID, Image: in.Image, Dockerfile: in.Dockerfile, Port: int64(in.Port),
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
		s.appEvent(app.ID, eventInfo, "DNS créé : "+host)
	}

	s.appEvent(app.ID, eventInfo, "Créée par "+currentUser(r).DisplayName+" sur le node "+node.Name)
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
	if in.Dockerfile != "" {
		in.Image = builtImage(a.Name, in.Dockerfile)
	}
	a, err = s.store.UpdateAppConfig(r.Context(), db.UpdateAppConfigParams{
		Image: in.Image, Dockerfile: in.Dockerfile, Port: int64(in.Port), EnvSealed: sealed, MemoryMb: int64(in.MemoryMB),
		UpdatedAt: time.Now().Unix(), ID: a.ID,
	})
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	source := a.Image
	if a.Dockerfile != "" {
		source = "Dockerfile"
	}
	s.appEvent(a.ID, eventInfo, "Configuration modifiée par "+currentUser(r).DisplayName+" : "+source)
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
	action := r.PathValue("action")
	if action == "start" || action == "redeploy" {
		owner, err := s.store.GetUserByID(r.Context(), a.OwnerID)
		if err != nil {
			s.internalError(w, r, err)
			return
		}
		if owner.AppsSuspended != 0 {
			writeError(w, http.StatusForbidden, errSuspended)
			return
		}
	}
	switch action {
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
	if params.Running == 1 {
		// Starting again gives a crash-suspended app a fresh count.
		if err := s.store.ClearCrashSuspension(r.Context(), a.ID); err != nil {
			s.internalError(w, r, err)
			return
		}
		a.CrashSuspended = 0
		s.mu.Lock()
		delete(s.crashes, a.ID)
		s.mu.Unlock()
	}
	verb := map[string]string{"start": "Démarrée", "stop": "Arrêtée", "redeploy": "Redéployée"}[action]
	s.appEvent(a.ID, eventInfo, verb+" par "+currentUser(r).DisplayName)
	s.logger.Info("app "+action, "app", a.Name, "by", currentUser(r).DisplayName)
	s.push(r.Context(), a.NodeID)
	s.writeApp(w, r, http.StatusOK, a, false)
}

func (s *Server) handleDeleteApp(w http.ResponseWriter, r *http.Request) {
	a, ok := s.appFromPath(w, r)
	if !ok {
		return
	}
	if err := s.removeApp(r.Context(), a); err != nil {
		s.internalError(w, r, err)
		return
	}
	s.logger.Info("app deleted", "app", a.Name, "by", currentUser(r).DisplayName)
	w.WriteHeader(http.StatusNoContent)
}

// removeApp deletes an app, the DNS record Forgeyard created for it, and then its container.
func (s *Server) removeApp(ctx context.Context, a db.App) error {
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
		return err
	}
	s.nodes.ForgetApp(a.ID)
	s.push(ctx, a.NodeID)
	if a.MovingFrom != 0 {
		s.push(ctx, a.MovingFrom) // it still ran there too
	}
	return nil
}

// handleAppLogs streams an app's output as server-sent events, starting with the last lines.
func (s *Server) handleAppLogs(w http.ResponseWriter, r *http.Request) {
	a, ok := s.appFromPath(w, r)
	if !ok {
		return
	}
	lines, err := s.nodes.Logs(r.Context(), a.NodeID, a.ID, "", logTail(r))
	s.streamLogs(w, r, lines, err)
}

func logTail(r *http.Request) int {
	tail, _ := strconv.Atoi(r.URL.Query().Get("tail"))
	if tail <= 0 || tail > 2000 {
		tail = 200
	}
	return tail
}

// streamLogs relays a container's log lines as server-sent events.
func (s *Server) streamLogs(w http.ResponseWriter, r *http.Request, lines <-chan *agentpb.LogLine, err error) {
	if errors.Is(err, nodes.ErrOffline) {
		writeError(w, http.StatusServiceUnavailable, "le node de ce conteneur est hors ligne")
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
