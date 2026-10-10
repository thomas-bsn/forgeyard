package api

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/thomas-bsn/forgeyard/internal/agentpb"
	"github.com/thomas-bsn/forgeyard/internal/nodes"
	"github.com/thomas-bsn/forgeyard/internal/store/db"
)

// Topology: how a request reaches an app (the app's Réseau tab), and how the whole infrastructure is
// wired (Nodes › Topologie and Tableau, admins). Each step of a request's path is checked against what
// is known: the DNS record, the node's ingress mode and relay, its Traefik, the app's state and the ports
// it really listens on. Sharing a Docker network only means two containers can talk, never that they do.

// pathStep is one hop of a request, checked.
type pathStep struct {
	Key    string `json:"key"` // visitor, dns, entry, relay, traefik, container, gateway, node
	Title  string `json:"title"`
	Detail string `json:"detail"`
	State  string `json:"state"` // ok, warn, error, info (not checkable), unknown (not measured)
	Note   string `json:"note"`
}

// diagnosis is a problem found on the path, with how to fix it.
type diagnosis struct {
	Level   string `json:"level"` // error or warn
	Message string `json:"message"`
	Help    string `json:"help,omitempty"`
	FixPort int32  `json:"fixPort,omitempty"` // set the app's port to this one
}

// topoContext is what the checks need, loaded once per request.
type topoContext struct {
	c        dnsConfig
	nodes    map[int64]db.Node
	local    *db.Node
	live     map[int64]nodes.Live
	relayed  map[string]bool // hostnames Forgeyard's machine relays to another node
	sshPort  int
	lookup   func(ctx context.Context, host string) ([]string, error)
	statusOf func(appID int64) (nodes.AppStatus, bool)
}

func (s *Server) loadTopoContext(ctx context.Context) (*topoContext, error) {
	c, err := s.loadDNSConfig(ctx)
	if err != nil {
		return nil, err
	}
	list, err := s.store.ListNodes(ctx)
	if err != nil {
		return nil, err
	}
	t := &topoContext{c: c, nodes: map[int64]db.Node{}, live: map[int64]nodes.Live{}, relayed: map[string]bool{},
		sshPort: s.sshPort, lookup: s.lookupHost, statusOf: s.nodes.AppStatus}
	for _, n := range list {
		t.nodes[n.ID] = n
		if n.IsLocal != 0 {
			local := n
			t.local = &local
		}
		if live, ok := s.nodes.Live(n.ID); ok {
			t.live[n.ID] = live
		}
	}
	if t.local != nil {
		relays, err := s.relays(ctx, *t.local, c)
		if err != nil {
			return nil, err
		}
		for _, r := range relays {
			t.relayed[r.GetHostname()] = true
		}
	}
	return t, nil
}

// appContainer is an app's container in its node's topology (the running version, not one rolling out).
func (t *topoContext) appContainer(a db.App) *agentpb.TopoContainer {
	live, ok := t.live[a.NodeID]
	if !ok || live.Topology == nil {
		return nil
	}
	for _, c := range live.Topology.GetContainers() {
		if c.GetAppId() == a.ID && !strings.HasSuffix(c.GetName(), "-next") {
			return c
		}
	}
	return nil
}

// listening is what an app really listens on: from its status, else from the topology.
func (t *topoContext) listening(a db.App) []int32 {
	if st, ok := t.statusOf(a.ID); ok && len(st.Status.GetListeningPorts()) > 0 {
		return st.Status.GetListeningPorts()
	}
	if c := t.appContainer(a); c != nil {
		return c.GetListening()
	}
	return nil
}

// traefikRunning tells whether a node's Traefik runs, when its topology is known.
func (t *topoContext) traefikRunning(nodeID int64) (running, known bool) {
	live, ok := t.live[nodeID]
	if !ok || live.Topology == nil {
		return false, false
	}
	for _, c := range live.Topology.GetContainers() {
		if c.GetRole() == "traefik" {
			return c.GetState() == "running", true
		}
	}
	return false, true
}

var stateWords = map[string]string{
	"pending": "en attente", "pulling": "téléchargement de l'image", "building": "construction de l'image",
	"creating": "démarrage", "deploying": "mise à jour", "restarting": "redémarre en boucle", "stopped": "arrêtée",
	"exited": "plantée", "error": "en erreur", "node-offline": "node hors ligne",
}

// appPath checks every hop from a visitor to an app. withDNS also resolves its name, which takes time:
// left out of the views that check every app at once.
func (t *topoContext) appPath(ctx context.Context, a db.App, withDNS bool) ([]pathStep, []diagnosis) {
	if a.Kind == kindSandbox {
		return t.sandboxPath(a)
	}
	var steps []pathStep
	var diags []diagnosis
	node := t.nodes[a.NodeID]
	_, online := t.live[node.ID]
	host := webHost(t.c, a)
	ip := nodeIP(node, t.c)
	port := strconv.FormatInt(node.IngressHttpPort, 10)

	if host == "" {
		steps = append(steps, pathStep{Key: "visitor", Title: "Visiteur", Detail: "pas de domaine", State: "info", Note: "port publié sur le node"})
	} else {
		steps = append(steps, pathStep{Key: "visitor", Title: "Visiteur", Detail: host, State: "info", Note: "HTTPS"})
		if withDNS {
			steps = append(steps, t.dnsStep(ctx, host, ip, &diags))
		}
		// A node receiving its visits directly behind the same public IP as Forgeyard's machine: the box
		// sends ports 80 and 443 to one machine only, Forgeyard's.
		shares := t.local != nil && t.local.ID != node.ID && node.Relayed == 0 && ip != "" && nodeIP(*t.local, t.c) == ip
		switch {
		case node.IngressMode == "traefik" && shares:
			steps = append(steps, pathStep{Key: "entry", Title: "Box → " + t.local.Name, Detail: ip + " :80 :443", State: "error", Note: "pas vers ce node"})
			diags = append(diags, diagnosis{Level: "error",
				Message: "Ce node reçoit ses visites directement, mais il a la même IP publique que la machine de Forgeyard : la box envoie les ports 80 et 443 à celle-ci, qui ne connaît pas l'app (404).",
				Help:    "Dans Nodes › Réseau, passez " + node.Name + " en « Relais par " + t.local.Name + " », ou donnez-lui sa propre IP publique."})
		case node.IngressMode == "traefik":
			steps = append(steps, pathStep{Key: "entry", Title: "Box → " + node.Name, Detail: ip + " :80 :443", State: "info", Note: "redirection de la box"})
		case node.IsLocal != 0:
			steps = append(steps, pathStep{Key: "entry", Title: "Ton reverse proxy", Detail: "→ :" + port, State: "info", Note: "Caddy, Nginx…"})
		case node.Relayed != 0 && t.local != nil:
			steps = append(steps, pathStep{Key: "entry", Title: "Box → " + t.local.Name, Detail: ip + " :443", State: "info", Note: "machine de Forgeyard"})
			relay := pathStep{Key: "relay", Title: "Relais · " + t.local.Name, Detail: "→ " + node.LocalIp + ":" + port, State: "ok", Note: "relais en place"}
			_, localOnline := t.live[t.local.ID]
			switch {
			case node.LocalIp == "":
				relay.State, relay.Note, relay.Detail = "error", "IP locale inconnue", "→ ?:"+port
				diags = append(diags, diagnosis{Level: "error", Message: "L'IP locale de " + node.Name + " est inconnue : la machine de Forgeyard ne sait pas où relayer ses apps.",
					Help: "Elle est trouvée par l'agent quand il se connecte : vérifiez qu'il est en ligne."})
			case !t.relayed[host]:
				relay.State, relay.Note = "error", "relais absent"
				diags = append(diags, diagnosis{Level: "error", Message: "La machine de Forgeyard ne relaie pas encore cette app.", Help: "Mettez-la à jour : le relais suit les apps des autres nodes."})
			case !localOnline:
				relay.State, relay.Note = "error", "hors ligne"
				diags = append(diags, diagnosis{Level: "error", Message: "La machine de Forgeyard est hors ligne : elle ne relaie plus les visites vers " + node.Name + "."})
			}
			steps = append(steps, relay)
		default:
			steps = append(steps, pathStep{Key: "entry", Title: "Ton reverse proxy", Detail: "→ " + node.LocalIp + ":" + port, State: "info", Note: "à configurer chez toi"})
		}
		tr := pathStep{Key: "traefik", Title: "Traefik · " + node.Name, Detail: "route → :" + strconv.FormatInt(a.Port, 10), State: "ok", Note: "route en place"}
		if running, known := t.traefikRunning(node.ID); !online {
			tr.State, tr.Note = "error", "node hors ligne"
		} else if known && !running {
			tr.State, tr.Note = "error", "Traefik arrêté"
			diags = append(diags, diagnosis{Level: "error", Message: "Traefik ne tourne pas sur " + node.Name + " : aucune app de ce node n'est joignable.", Help: "L'agent le relance tout seul ; regardez ses logs s'il n'y arrive pas."})
		}
		steps = append(steps, tr)
	}
	if !online {
		diags = append(diags, diagnosis{Level: "error", Message: "Le node " + node.Name + " est hors ligne : ses apps ne répondent plus."})
	}

	ct := pathStep{Key: "container", Title: a.Name, State: "ok"}
	if c := t.appContainer(a); c != nil {
		for _, ep := range c.GetEndpoints() {
			if ep.GetNetwork() == "forgeyard" {
				ct.Detail = ep.GetIpv4()
			}
		}
	}
	st, known := t.statusOf(a.ID)
	listening := t.listening(a)
	switch state := st.Status.GetState(); {
	case a.Running == 0:
		ct.State, ct.Note = "error", "arrêtée"
		diags = append(diags, diagnosis{Level: "error", Message: "L'app est arrêtée.", Help: "Démarrez-la depuis sa page."})
	case !known || state != "running":
		word := stateWords[state]
		if word == "" {
			word = "état inconnu"
		}
		ct.State, ct.Note = "error", word
		if known {
			diags = append(diags, diagnosis{Level: "error", Message: "L'app n'est pas en ligne : " + word + ".", Help: "Regardez ses logs et ses événements."})
		}
	case len(listening) == 0:
		ct.State, ct.Note = "unknown", "écoute non mesurée"
	case !slices.Contains(listening, int32(a.Port)):
		ct.State, ct.Note = "error", "rien sur :"+strconv.FormatInt(a.Port, 10)
		d := diagnosis{Level: "error",
			Message: "Traefik envoie les visites sur le port " + strconv.FormatInt(a.Port, 10) + ", mais " + a.Name + " écoute sur " + joinPorts(listening) + " : c'est ce qui donne « Bad Gateway ».",
			FixPort: listening[0]}
		diags = append(diags, d)
	default:
		ct.Note = "écoute :" + strconv.FormatInt(a.Port, 10)
	}
	if ct.Detail == "" {
		ct.Detail = "port " + strconv.FormatInt(a.Port, 10)
	} else if len(listening) > 0 {
		ct.Detail += " · écoute " + joinPorts(listening)
	}
	steps = append(steps, ct)
	return steps, diags
}

func (t *topoContext) dnsStep(ctx context.Context, host, want string, diags *[]diagnosis) pathStep {
	step := pathStep{Key: "dns", Title: "DNS", State: "ok"}
	lctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	ips, err := t.lookup(lctx, host)
	cancel()
	switch {
	case err != nil || len(ips) == 0:
		step.State, step.Detail, step.Note = "error", host, "ne répond pas"
		help := "Ajoutez un enregistrement A pour ce nom, ou *." + t.c.Domain + ", vers " + want + "."
		if t.c.Mode == "provider" {
			help = "Forgeyard le crée à la création de l'app : vérifiez Réglages › Domaine, puis redéployez."
		}
		*diags = append(*diags, diagnosis{Level: "error", Message: "Le nom " + host + " ne mène nulle part.", Help: help})
	case slices.Contains(ips, want) || want == "":
		step.Detail, step.Note = "A "+strings.Join(ips, ", "), "pointe vers ta box"
	case allCloudflare(ips):
		step.Detail, step.Note = "via Cloudflare", "proxy Cloudflare"
	default:
		step.State, step.Detail, step.Note = "warn", "A "+strings.Join(ips, ", "), "attendu "+want
		*diags = append(*diags, diagnosis{Level: "warn", Message: host + " pointe vers " + strings.Join(ips, ", ") + " au lieu de " + want + ".",
			Help: "Le DNS met jusqu'à quelques minutes à suivre un changement ; sinon, corrigez l'enregistrement."})
	}
	return step
}

func (t *topoContext) sandboxPath(a db.App) ([]pathStep, []diagnosis) {
	var diags []diagnosis
	node := t.nodes[a.NodeID]
	steps := []pathStep{{Key: "visitor", Title: "Ton ordinateur", Detail: "ssh " + a.Name + "@…", State: "info", Note: "clé de ton profil"}}
	gw := pathStep{Key: "gateway", Title: "Passerelle SSH", Detail: ":" + strconv.Itoa(t.sshPort), State: "ok", Note: "machine de Forgeyard"}
	if t.sshPort == 0 {
		gw.State, gw.Detail, gw.Note = "error", "désactivée", "FORGEYARD_SSH_ADDR=off"
		diags = append(diags, diagnosis{Level: "error", Message: "La passerelle SSH est désactivée : seul l'onglet Terminal donne accès à la sandbox."})
	}
	steps = append(steps, gw)
	ag := pathStep{Key: "node", Title: "Agent · " + node.Name, Detail: "flux de l'agent", State: "ok", Note: "en ligne"}
	if _, ok := t.live[node.ID]; !ok {
		ag.State, ag.Note = "error", "hors ligne"
		diags = append(diags, diagnosis{Level: "error", Message: "Le node " + node.Name + " est hors ligne."})
	}
	steps = append(steps, ag)
	ct := pathStep{Key: "container", Title: a.Name, Detail: a.Image, State: "ok", Note: "en ligne"}
	if st, ok := t.statusOf(a.ID); a.Running == 0 || !ok || st.Status.GetState() != "running" {
		ct.State, ct.Note = "error", "pas en ligne"
		diags = append(diags, diagnosis{Level: "error", Message: "La sandbox n'est pas en ligne.", Help: "Démarrez-la depuis sa page."})
	}
	return append(steps, ct), diags
}

func joinPorts(ports []int32) string {
	if len(ports) == 0 {
		return "?"
	}
	out := make([]string, len(ports))
	for i, p := range ports {
		out[i] = strconv.Itoa(int(p))
	}
	return strings.Join(out, ", ")
}

func allCloudflare(ips []string) bool {
	for _, s := range ips {
		addr, err := netip.ParseAddr(s)
		if err != nil || !slices.ContainsFunc(CloudflareProxies, func(p netip.Prefix) bool { return p.Contains(addr.Unmap()) }) {
			return false
		}
	}
	return len(ips) > 0
}

// --- The app's Réseau tab ---

type topoEndpoint struct {
	Network string   `json:"network"`
	Subnet  string   `json:"subnet,omitempty"`
	IP      string   `json:"ip,omitempty"`
	Aliases []string `json:"aliases,omitempty"`
}

type topoPort struct {
	ContainerPort int32  `json:"containerPort"`
	HostPort      int32  `json:"hostPort"`
	Protocol      string `json:"protocol"`
}

type appNetworkResponse struct {
	URL       string         `json:"url,omitempty"`
	Kind      string         `json:"kind"`
	Steps     []pathStep     `json:"steps"`
	Issues    []diagnosis    `json:"issues"`
	CheckedAt int64          `json:"checkedAt"`
	Networks  []topoEndpoint `json:"networks"`
	// Neighbors are the apps of the same owner sharing one of its networks (they can reach it by its
	// name), Others how many other containers do.
	Neighbors []string   `json:"neighbors"`
	Others    int        `json:"others"`
	Listening []int32    `json:"listening"`
	Published []topoPort `json:"published"`
	RoutePort int64      `json:"routePort,omitempty"`
	Measured  bool       `json:"measured"` // the node sent its topology
}

func (s *Server) handleAppNetwork(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	a, ok := s.appFromPath(w, r)
	if !ok {
		return
	}
	t, err := s.loadTopoContext(ctx)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	steps, issues := t.appPath(ctx, a, true)
	out := appNetworkResponse{Kind: a.Kind, Steps: steps, Issues: issues, CheckedAt: time.Now().Unix(),
		Networks: []topoEndpoint{}, Neighbors: []string{}, Listening: t.listening(a), Published: []topoPort{}}
	if out.Issues == nil {
		out.Issues = []diagnosis{}
	}
	if host := webHost(t.c, a); host != "" {
		out.URL, out.RoutePort = "https://"+host, a.Port
	}
	if out.Listening == nil {
		out.Listening = []int32{}
	}
	c := t.appContainer(a)
	live := t.live[a.NodeID]
	if c == nil || live.Topology == nil {
		writeJSON(w, http.StatusOK, out)
		return
	}
	out.Measured = true
	subnets := map[string]string{}
	for _, n := range live.Topology.GetNetworks() {
		subnets[n.GetName()] = n.GetSubnet()
	}
	mine := map[string]bool{}
	for _, ep := range c.GetEndpoints() {
		out.Networks = append(out.Networks, topoEndpoint{Network: ep.GetNetwork(), Subnet: subnets[ep.GetNetwork()], IP: ep.GetIpv4(), Aliases: ep.GetAliases()})
		mine[ep.GetNetwork()] = true
	}
	for _, p := range c.GetPublished() {
		out.Published = append(out.Published, topoPort{ContainerPort: p.GetContainerPort(), HostPort: p.GetHostPort(), Protocol: p.GetProtocol()})
	}
	// Who shares a network with it: the owner's own apps by name, the rest counted, as they may belong to
	// someone else.
	apps, err := s.store.ListAppsByNode(ctx, a.NodeID)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	byID := map[int64]db.App{}
	for _, x := range apps {
		byID[x.ID] = x
	}
	for _, other := range live.Topology.GetContainers() {
		if other.GetId() == c.GetId() || other.GetState() != "running" || !slices.ContainsFunc(other.GetEndpoints(), func(ep *agentpb.NetworkEndpoint) bool { return mine[ep.GetNetwork()] }) {
			continue
		}
		if x, ok := byID[other.GetAppId()]; ok && (x.OwnerID == a.OwnerID || isAdmin(currentUser(r))) {
			out.Neighbors = append(out.Neighbors, x.Name)
		} else if other.GetRole() == "traefik" {
			out.Neighbors = append(out.Neighbors, "traefik")
		} else {
			out.Others++
		}
	}
	slices.Sort(out.Neighbors)
	writeJSON(w, http.StatusOK, out)
}

type probeResult struct {
	OK     bool   `json:"ok"`
	Status int    `json:"status,omitempty"`
	Error  string `json:"error,omitempty"`
	Millis int64  `json:"ms"`
}

// handleAppProbe requests the app's address from Forgeyard's machine, as a visitor would.
func (s *Server) handleAppProbe(w http.ResponseWriter, r *http.Request) {
	a, ok := s.appFromPath(w, r)
	if !ok {
		return
	}
	c, err := s.loadDNSConfig(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	host := webHost(c, a)
	if host == "" {
		writeError(w, http.StatusBadRequest, "cette app n'a pas d'adresse web")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+host+"/", nil)
	start := time.Now()
	resp, err := s.probeClient.Do(req)
	out := probeResult{Millis: time.Since(start).Milliseconds()}
	var certErr *tls.CertificateVerificationError
	switch {
	case errors.As(err, &certErr):
		out.Error = "certificat HTTPS refusé : " + certErr.Err.Error()
	case err != nil:
		var opErr *net.OpError
		out.Error = err.Error()
		if errors.As(err, &opErr) {
			out.Error = "connexion impossible (" + opErr.Err.Error() + ") : une box ne laisse pas toujours une machine de chez toi se joindre par l'IP publique, essayez aussi depuis un téléphone en 4G"
		}
	default:
		resp.Body.Close()
		out.Status = resp.StatusCode
		out.OK = resp.StatusCode < 500 && resp.StatusCode != http.StatusNotFound
	}
	writeJSON(w, http.StatusOK, out)
}

// --- Nodes › Topologie and Tableau (admins) ---

type topoContainer struct {
	ID             string         `json:"id"`
	Name           string         `json:"name"`
	Image          string         `json:"image"`
	State          string         `json:"state"`
	Role           string         `json:"role"` // app, sandbox, traefik, server, agent, external
	AppID          int64          `json:"appId,omitempty"`
	OwnerName      string         `json:"ownerName,omitempty"`
	URL            string         `json:"url,omitempty"`
	RoutePort      int64          `json:"routePort,omitempty"`
	Listening      []int32        `json:"listening"`
	Published      []topoPort     `json:"published"`
	Endpoints      []topoEndpoint `json:"endpoints"`
	ComposeProject string         `json:"composeProject,omitempty"`
	Issue          *diagnosis     `json:"issue,omitempty"`
	Steps          []pathStep     `json:"steps,omitempty"` // an app's path, to light it up when selected
	LogoURL        string         `json:"logoUrl,omitempty"`
	LogoColor      string         `json:"logoColor,omitempty"`
}

type topoNetwork struct {
	Name     string `json:"name"`
	Driver   string `json:"driver"`
	Subnet   string `json:"subnet,omitempty"`
	Internal bool   `json:"internal,omitempty"`
}

type topoNode struct {
	ID          int64           `json:"id"`
	Name        string          `json:"name"`
	State       string          `json:"state"`
	IsLocal     bool            `json:"isLocal"`
	IngressMode string          `json:"ingressMode"`
	HTTPPort    int64           `json:"httpPort"`
	LocalIP     string          `json:"localIp,omitempty"`
	PublicIP    string          `json:"publicIp,omitempty"`
	RelayedBy   int64           `json:"relayedBy,omitempty"` // Forgeyard's machine, relaying its apps
	Measured    bool            `json:"measured"`
	Networks    []topoNetwork   `json:"networks"`
	Containers  []topoContainer `json:"containers"`
}

type topologyResponse struct {
	Domain    string     `json:"domain,omitempty"`
	PublicIP  string     `json:"publicIp,omitempty"`
	SSHPort   int        `json:"sshPort,omitempty"`
	Nodes     []topoNode `json:"nodes"`
	CheckedAt int64      `json:"checkedAt"`
}

func (s *Server) handleTopology(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	t, err := s.loadTopoContext(ctx)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	rows, err := s.store.ListApps(ctx)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	apps := map[int64]db.ListAppsRow{}
	for _, row := range rows {
		apps[row.App.ID] = row
	}
	out := topologyResponse{Domain: t.c.Domain, PublicIP: t.c.PublicIP, SSHPort: t.sshPort, Nodes: []topoNode{}, CheckedAt: time.Now().Unix()}
	list, err := s.store.ListNodes(ctx)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	for _, n := range list {
		if n.Status != "active" {
			continue
		}
		live, online := t.live[n.ID]
		tn := topoNode{ID: n.ID, Name: n.Name, State: "offline", IsLocal: n.IsLocal != 0, IngressMode: ingressMode(n),
			HTTPPort: n.IngressHttpPort, LocalIP: n.LocalIp, PublicIP: nodeIP(n, t.c), Networks: []topoNetwork{}, Containers: []topoContainer{}}
		if online {
			tn.State = "online"
		}
		if t.local != nil && t.local.ID != n.ID && n.Relayed != 0 {
			tn.RelayedBy = t.local.ID
		}
		if online && live.Topology != nil {
			tn.Measured = true
			used := map[string]bool{}
			for _, c := range live.Topology.GetContainers() {
				for _, ep := range c.GetEndpoints() {
					used[ep.GetNetwork()] = true
				}
			}
			subnets := map[string]string{}
			for _, net := range live.Topology.GetNetworks() {
				subnets[net.GetName()] = net.GetSubnet()
				// Docker's default networks without anyone on them only add noise.
				if used[net.GetName()] {
					tn.Networks = append(tn.Networks, topoNetwork{Name: net.GetName(), Driver: net.GetDriver(), Subnet: net.GetSubnet(), Internal: net.GetInternal()})
				}
			}
			for _, c := range live.Topology.GetContainers() {
				tc := topoContainer{ID: c.GetId(), Name: c.GetName(), Image: c.GetImage(), State: c.GetState(), Role: c.GetRole(),
					ComposeProject: c.GetComposeProject(), Listening: c.GetListening(), Published: []topoPort{}, Endpoints: []topoEndpoint{},
					LogoURL: imageLogoURL(c.GetImage())}
				if tc.Listening == nil {
					tc.Listening = []int32{}
				}
				for _, ep := range c.GetEndpoints() {
					tc.Endpoints = append(tc.Endpoints, topoEndpoint{Network: ep.GetNetwork(), Subnet: subnets[ep.GetNetwork()], IP: ep.GetIpv4(), Aliases: ep.GetAliases()})
				}
				for _, p := range c.GetPublished() {
					tc.Published = append(tc.Published, topoPort{ContainerPort: p.GetContainerPort(), HostPort: p.GetHostPort(), Protocol: p.GetProtocol()})
				}
				if row, ok := apps[c.GetAppId()]; ok && c.GetRole() == "app" {
					if strings.HasSuffix(c.GetName(), "-next") {
						continue // the next version of an app rolling out: shown once it took over
					}
					a := row.App
					tc.AppID, tc.Name, tc.OwnerName = a.ID, a.Name, row.OwnerName
					logo := toAppLogo(a)
					tc.LogoURL, tc.LogoColor = logo.URL, logo.Color
					if a.Kind == kindSandbox {
						tc.Role = "sandbox"
					} else if host := webHost(t.c, a); host != "" {
						tc.URL, tc.RoutePort = "https://"+host, a.Port
					}
					steps, diags := t.appPath(ctx, a, false)
					tc.Steps = steps
					if len(diags) > 0 {
						d := diags[0]
						tc.Issue = &d
					}
				}
				if tc.Role == "traefik" && tc.State != "running" {
					tc.Issue = &diagnosis{Level: "error", Message: "Traefik ne tourne pas : aucune app de ce node n'est joignable."}
				}
				tn.Containers = append(tn.Containers, tc)
			}
		}
		out.Nodes = append(out.Nodes, tn)
	}
	writeJSON(w, http.StatusOK, out)
}
