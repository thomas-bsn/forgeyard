package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/thomas-bsn/forgeyard/internal/agentpb"
	"github.com/thomas-bsn/forgeyard/internal/compose"
	"github.com/thomas-bsn/forgeyard/internal/dns"
	"github.com/thomas-bsn/forgeyard/internal/store/db"
)

// A stack is a docker-compose.yml and its variables, run on one node. Each service is an app of the stack:
// the same deployment, logs, terminal and events as any app, its configuration coming from the compose
// file. The exposed service is named after the stack (its subdomain), the others <stack>-<service>.

const (
	settingStackEnvLabel = "stack_env"

	kindService = "service" // a service of a stack only the stack reaches
	kindJob     = "job"     // a service of a stack another one waits to complete

	maxComposeBytes = 128 << 10
)

// stackNetwork is the Docker network of a stack, where its services reach each other by name.
func stackNetwork(stackID int64) string {
	return "forgeyard-stack-" + strconv.FormatInt(stackID, 10)
}

// stackVolume is the Docker volume of a named volume of a stack, unless a restore put another in its place.
func stackVolume(st db.Stack, name string) string {
	if v := stackOverrides(st)[name]; v != "" {
		return v
	}
	return "forgeyard-stack-" + strconv.FormatInt(st.ID, 10) + "-" + name
}

func stackOverrides(st db.Stack) map[string]string {
	out := map[string]string{}
	json.Unmarshal([]byte(st.VolumeOverrides), &out)
	return out
}

// stackAppName is the app of a service: the stack's name for its exposed service, else <stack>-<service>.
func stackAppName(stack string, svc compose.Service) string {
	if svc.Expose > 0 {
		return stack
	}
	return stack + "-" + svc.Name
}

// plannedApp is what an app of a stack must be, from its compose file.
type plannedApp struct {
	Name     string
	Service  string
	Kind     string
	Image    string
	Port     int64
	Env      map[string]string
	MemoryMB int64
	Spec     string // the service's JSON, how it runs
}

// planServices turns a parsed compose file into the stack's apps, in start order.
func planServices(stack string, st *compose.Stack) []plannedApp {
	out := make([]plannedApp, 0, len(st.Services))
	for _, svc := range st.Services {
		p := plannedApp{Name: stackAppName(stack, svc), Service: svc.Name, Kind: kindService, Image: svc.Image,
			Port: int64(svc.Expose), Env: svc.Environment, MemoryMB: int64(svc.MemoryMB), Spec: string(must(json.Marshal(svc)))}
		switch {
		case svc.Expose > 0:
			p.Kind = kindWeb
		case svc.Job:
			p.Kind = kindJob
		}
		if !strings.Contains(lastPathPart(p.Image), ":") && !strings.Contains(p.Image, "@") {
			p.Image += ":latest"
		}
		if p.MemoryMB == 0 {
			p.MemoryMB = 512
		}
		out = append(out, p)
	}
	return out
}

func lastPathPart(image string) string {
	if i := strings.LastIndex(image, "/"); i >= 0 {
		return image[i+1:]
	}
	return image
}

// stackApp is an app of a stack as stored, its variables decrypted.
type stackApp struct {
	App db.App
	Env map[string]string
}

// stackPlan is what an update of a stack changes: only the services whose configuration changed are
// deployed again. A service that changes name (it became, or stopped being, the exposed one) is replaced.
type stackPlan struct {
	Create []plannedApp
	Update []stackUpdate
	Remove []db.App
	Same   []db.App
}

type stackUpdate struct {
	App  db.App
	Want plannedApp
}

func diffStack(existing []stackApp, want []plannedApp) stackPlan {
	var plan stackPlan
	byService := map[string]stackApp{}
	for _, e := range existing {
		byService[e.App.Service] = e
	}
	seen := map[string]bool{}
	for _, w := range want {
		seen[w.Service] = true
		e, ok := byService[w.Service]
		switch {
		case !ok:
			plan.Create = append(plan.Create, w)
		case e.App.Name != w.Name || e.App.Kind != w.Kind:
			plan.Remove = append(plan.Remove, e.App)
			plan.Create = append(plan.Create, w)
		case e.App.Image != w.Image || e.App.Port != w.Port || e.App.MemoryMb != w.MemoryMB || e.App.ServiceSpec != w.Spec || !maps.Equal(e.Env, w.Env):
			plan.Update = append(plan.Update, stackUpdate{App: e.App, Want: w})
		default:
			plan.Same = append(plan.Same, e.App)
		}
	}
	for _, e := range existing {
		if !seen[e.App.Service] {
			plan.Remove = append(plan.Remove, e.App)
		}
	}
	return plan
}

// stackInput is what users set when creating or editing a stack.
type stackInput struct {
	Name    string            `json:"name"`
	Compose string            `json:"compose"`
	Env     map[string]string `json:"env"`
	NodeID  int64             `json:"nodeId"` // at creation: where it runs, 0 for the recommended node
}

// stackErrors answers a compose file that cannot run, with every problem and its line.
type stackErrors struct {
	Error  string         `json:"error"`
	Errors compose.Errors `json:"errors"`
}

// parse checks a stack's input: its variables, then its compose file. The problems of the file come with
// their lines.
func (in *stackInput) parse(creating bool) (*compose.Stack, string, compose.Errors) {
	in.Name = strings.ToLower(strings.TrimSpace(in.Name))
	in.Compose = strings.ReplaceAll(in.Compose, "\r\n", "\n")
	if creating && !appNamePattern.MatchString(in.Name) {
		return nil, "nom : 1 à 32 caractères parmi a-z, 0-9 et -, sans tiret au début ni à la fin (il devient le sous-domaine du service exposé)", nil
	}
	if len(in.Compose) > maxComposeBytes {
		return nil, "docker-compose.yml trop long : 128 Ko au maximum", nil
	}
	if in.Env == nil {
		in.Env = map[string]string{}
	}
	if len(in.Env) > 100 {
		return nil, "100 variables au maximum", nil
	}
	for k, v := range in.Env {
		if !envKeyPattern.MatchString(k) {
			return nil, "nom de variable invalide : " + k, nil
		}
		if len(v) > 4096 {
			return nil, "valeur trop longue pour la variable " + k, nil
		}
	}
	st, errs := compose.Parse([]byte(in.Compose), in.Env)
	if errs != nil {
		return nil, "", errs
	}
	if len(st.Services) > 20 {
		return nil, "", compose.Errors{{Message: "20 services au maximum par projet"}}
	}
	if in.Name != "" {
		for _, svc := range st.Services {
			if name := stackAppName(in.Name, svc); len(name) > 32 {
				errs = append(errs, compose.Error{Message: "le service " + svc.Name + " donnerait l'app " + name + " : 32 caractères au maximum, raccourcissez le nom du projet ou du service"})
			}
		}
	}
	if errs != nil {
		return nil, "", errs
	}
	return st, "", nil
}

func writeStackErrors(w http.ResponseWriter, errs compose.Errors) {
	writeJSON(w, http.StatusBadRequest, stackErrors{Error: "docker-compose.yml non pris en charge : " + strconv.Itoa(len(errs)) + " problème(s)", Errors: errs})
}

func (s *Server) sealStackEnv(env map[string]string) (string, error) {
	if len(env) == 0 {
		return "", nil
	}
	return s.secrets.Encrypt(string(must(json.Marshal(env))), settingStackEnvLabel)
}

func (s *Server) openStackEnv(sealed string) (map[string]string, error) {
	env := map[string]string{}
	if sealed == "" {
		return env, nil
	}
	plain, err := s.secrets.Decrypt(sealed, settingStackEnvLabel)
	if err != nil {
		return nil, err
	}
	return env, json.Unmarshal([]byte(plain), &env)
}

// stackServiceSpec is how the agent runs a service of a stack: its dependencies are the apps of the
// services it names.
func stackServiceSpec(st db.Stack, a db.App, appIDs map[string]int64) *agentpb.StackService {
	var svc compose.Service
	json.Unmarshal([]byte(a.ServiceSpec), &svc)
	out := &agentpb.StackService{
		StackId: st.ID, Network: stackNetwork(st.ID), Service: a.Service, Command: svc.Command, Entrypoint: svc.Entrypoint,
		Restart: svc.Restart, Job: svc.Job,
	}
	if h := svc.Healthcheck; h != nil {
		out.Healthcheck = &agentpb.Healthcheck{Test: h.Test, IntervalNs: int64(h.Interval), TimeoutNs: int64(h.Timeout),
			StartPeriodNs: int64(h.StartPeriod), Retries: int32(h.Retries)}
	}
	for _, d := range svc.DependsOn {
		out.DependsOn = append(out.DependsOn, &agentpb.Dependency{AppId: appIDs[d.Service], Service: d.Service, Condition: d.Condition})
	}
	for _, m := range svc.Volumes {
		out.Volumes = append(out.Volumes, &agentpb.NamedVolume{Name: stackVolume(st, m.Volume), Target: m.Target, ReadOnly: m.ReadOnly, Volume: m.Volume})
	}
	return out
}

// stackSpecs gives the stack part of the apps of a node's stacks, by app.
func (s *Server) stackSpecs(ctx context.Context, apps []db.App) (map[int64]*agentpb.StackService, error) {
	out := map[int64]*agentpb.StackService{}
	stacks := map[int64][]db.App{}
	for _, a := range apps {
		if a.StackID != 0 {
			stacks[a.StackID] = nil
		}
	}
	for id := range stacks {
		st, err := s.store.GetStack(ctx, id)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		members, err := s.store.ListStackApps(ctx, id)
		if err != nil {
			return nil, err
		}
		ids := map[string]int64{}
		for _, m := range members {
			ids[m.Service] = m.ID
		}
		for _, m := range members {
			out[m.ID] = stackServiceSpec(st, m, ids)
		}
	}
	return out, nil
}

// stackVolumeInfo is a named volume of a stack.
type stackVolumeInfo struct {
	Name      string   `json:"name"`
	Docker    string   `json:"docker"` // the Docker volume on the node
	SizeBytes int64    `json:"sizeBytes"`
	Services  []string `json:"services"`
	// Postgres: a service of type postgres uses it: it is backed up with pg_dump.
	Postgres string                `json:"postgres,omitempty"`
	Restored bool                  `json:"restored,omitempty"` // a restored volume took its place
	Backup   *backupPolicyResponse `json:"backup,omitempty"`
}

type stackResponse struct {
	ID        int64             `json:"id"`
	Name      string            `json:"name"`
	OwnerID   int64             `json:"ownerId"`
	OwnerName string            `json:"ownerName"`
	NodeID    int64             `json:"nodeId"`
	NodeName  string            `json:"nodeName"`
	URL       string            `json:"url,omitempty"`
	State     string            `json:"state"`
	Services  []appResponse     `json:"services"`
	Volumes   []stackVolumeInfo `json:"volumes"`
	UpdatedAt int64             `json:"updatedAt"`
	Compose   string            `json:"compose,omitempty"` // with the variables, for its owner
	Env       map[string]string `json:"env,omitempty"`
}

// stackState sums up a stack from its services: blocked when a job failed or a service cannot start,
// error when one crashed, deploying while one starts, running when all run (and the jobs completed).
func stackState(services []appResponse) string {
	if len(services) == 0 {
		return "pending"
	}
	has := func(states ...string) bool {
		return slices.ContainsFunc(services, func(a appResponse) bool { return slices.Contains(states, a.State) })
	}
	switch {
	case has("node-offline"):
		return "node-offline"
	case has("blocked", "failed"):
		return "blocked"
	case has("error", "exited", "restarting"):
		return "error"
	case !slices.ContainsFunc(services, func(a appResponse) bool { return a.Running }):
		return "stopped"
	case has("pending", "pulling", "creating", "deploying", "waiting", "building"):
		return "deploying"
	}
	return "running"
}

func canManageStack(u db.User, st db.Stack) bool {
	return isAdmin(u) || st.OwnerID == u.ID
}

// stackFromPath loads the stack named in the path, if the current user may manage it.
func (s *Server) stackFromPath(w http.ResponseWriter, r *http.Request) (db.Stack, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusNotFound, "projet introuvable")
		return db.Stack{}, false
	}
	st, err := s.store.GetStack(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !canManageStack(currentUser(r), st)) {
		writeError(w, http.StatusNotFound, "projet introuvable")
		return db.Stack{}, false
	}
	if err != nil {
		s.internalError(w, r, err)
		return db.Stack{}, false
	}
	return st, true
}

// describeStack builds a stack's response; withSource adds its compose file and variables.
func (s *Server) describeStack(ctx context.Context, r *http.Request, st db.Stack, ownerName, nodeName string, withSource bool) (stackResponse, error) {
	resp := stackResponse{ID: st.ID, Name: st.Name, OwnerID: st.OwnerID, OwnerName: ownerName, NodeID: st.NodeID, NodeName: nodeName,
		UpdatedAt: st.UpdatedAt, Services: []appResponse{}, Volumes: []stackVolumeInfo{}}
	c, err := s.loadDNSConfig(ctx)
	if err != nil {
		return resp, err
	}
	node, err := s.store.GetNode(ctx, st.NodeID)
	if err != nil {
		return resp, err
	}
	owner, err := s.store.GetUserByID(ctx, st.OwnerID)
	if err != nil {
		return resp, err
	}
	apps, err := s.store.ListStackApps(ctx, st.ID)
	if err != nil {
		return resp, err
	}
	byService := map[string]appResponse{}
	var specs = map[string]compose.Service{}
	for _, a := range apps {
		ar := s.toAppResponse(a, ownerName, owner.AppsSuspended != 0, nodeName, c, &node, s.publicHost(ctx, r))
		byService[a.Service] = ar
		var svc compose.Service
		json.Unmarshal([]byte(a.ServiceSpec), &svc)
		specs[a.Service] = svc
		if a.Kind == kindWeb && ar.URL != "" {
			resp.URL = ar.URL
		}
	}
	// Services in start order, as the compose file gives it.
	parsed, _ := compose.Order(func() []compose.Service {
		var list []compose.Service
		for name, svc := range specs {
			svc.Name = name
			list = append(list, svc)
		}
		return list
	}())
	for _, svc := range parsed {
		resp.Services = append(resp.Services, byService[svc.Name])
	}
	resp.State = stackState(resp.Services)

	policies, err := s.store.ListStackBackupPolicies(ctx, st.ID)
	if err != nil {
		return resp, err
	}
	sizes := map[string]int64{}
	if live, ok := s.nodes.Live(st.NodeID); ok && live.Topology != nil {
		for _, v := range live.Topology.GetVolumes() {
			if v.GetStackId() == st.ID {
				sizes[v.GetName()] = v.GetSizeBytes()
			}
		}
	}
	overrides := stackOverrides(st)
	volumes := map[string]*stackVolumeInfo{}
	var names []string
	for _, svc := range parsed {
		for _, m := range svc.Volumes {
			v, ok := volumes[m.Volume]
			if !ok {
				docker := stackVolume(st, m.Volume)
				v = &stackVolumeInfo{Name: m.Volume, Docker: docker, SizeBytes: sizeOr(sizes, docker), Restored: overrides[m.Volume] != ""}
				volumes[m.Volume] = v
				names = append(names, m.Volume)
			}
			v.Services = append(v.Services, svc.Name)
			if svc.Backup == "postgres" {
				v.Postgres = svc.Name
			}
		}
	}
	for _, name := range names {
		v := volumes[name]
		for _, p := range policies {
			if p.Volume == name {
				b := s.toBackupPolicy(p)
				v.Backup = &b
			}
		}
		resp.Volumes = append(resp.Volumes, *v)
	}
	if withSource {
		resp.Compose = st.Compose
		if resp.Env, err = s.openStackEnv(st.EnvSealed); err != nil {
			return resp, err
		}
	}
	return resp, nil
}

func (s *Server) writeStack(w http.ResponseWriter, r *http.Request, status int, st db.Stack) {
	ctx := r.Context()
	owner, err := s.store.GetUserByID(ctx, st.OwnerID)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	node, err := s.store.GetNode(ctx, st.NodeID)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	resp, err := s.describeStack(ctx, r, st, owner.DisplayName, node.Name, true)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, status, resp)
}

func (s *Server) handleListStacks(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := currentUser(r)
	var rows []db.ListStacksRow
	var err error
	if isAdmin(user) {
		rows, err = s.store.ListStacks(ctx)
	} else {
		var own []db.ListStacksByOwnerRow
		own, err = s.store.ListStacksByOwner(ctx, user.ID)
		for _, o := range own {
			rows = append(rows, db.ListStacksRow(o))
		}
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	out := make([]stackResponse, 0, len(rows))
	for _, row := range rows {
		resp, err := s.describeStack(ctx, r, row.Stack, row.OwnerName, row.NodeName, false)
		if err != nil {
			s.internalError(w, r, err)
			return
		}
		out = append(out, resp)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleGetStack(w http.ResponseWriter, r *http.Request) {
	if st, ok := s.stackFromPath(w, r); ok {
		s.writeStack(w, r, http.StatusOK, st)
	}
}

// stackPreview is what a compose file gives, shown while it is typed.
type stackPreview struct {
	Services []stackPreviewService `json:"services"`
	Volumes  []string              `json:"volumes"`
	Errors   compose.Errors        `json:"errors"`
	Error    string                `json:"error,omitempty"`
}

type stackPreviewService struct {
	Name      string               `json:"name"`
	App       string               `json:"app"`
	Image     string               `json:"image"`
	Kind      string               `json:"kind"`
	Port      int                  `json:"port,omitempty"`
	DependsOn []compose.Dependency `json:"dependsOn"`
	Backup    string               `json:"backup,omitempty"`
}

// handleValidateStack checks a compose file as it is typed, without saving anything.
func (s *Server) handleValidateStack(w http.ResponseWriter, r *http.Request) {
	var in stackInput
	if !decodeJSON(w, r, &in) {
		return
	}
	creating := in.Name != ""
	st, msg, errs := in.parse(creating)
	out := stackPreview{Services: []stackPreviewService{}, Volumes: []string{}, Errors: errs, Error: msg}
	if out.Errors == nil {
		out.Errors = compose.Errors{}
	}
	if st != nil {
		out.Volumes = st.Volumes
		for _, p := range planServices(in.Name, st) {
			var svc compose.Service
			json.Unmarshal([]byte(p.Spec), &svc)
			deps := svc.DependsOn
			if deps == nil {
				deps = []compose.Dependency{}
			}
			out.Services = append(out.Services, stackPreviewService{Name: p.Service, App: p.Name, Image: p.Image, Kind: p.Kind, Port: int(p.Port), DependsOn: deps, Backup: svc.Backup})
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// checkStackNames refuses app names already taken, reserved, or (for the exposed service) in the DNS zone.
func (s *Server) checkStackNames(ctx context.Context, r *http.Request, c dnsConfig, want []plannedApp, own map[string]bool) (int, string) {
	reserved := s.reservedNames(ctx, r, c)
	for _, p := range want {
		if own[p.Name] {
			continue
		}
		if reserved[p.Name] {
			return http.StatusConflict, p.Name + " est un nom réservé : renommez le projet"
		}
		taken, err := s.store.NameTaken(ctx, p.Name)
		if err != nil {
			return http.StatusInternalServerError, err.Error()
		}
		if taken != 0 {
			return http.StatusConflict, "une app porte déjà le nom " + p.Name + " : renommez le projet ou le service"
		}
		if prov, err := s.dnsProvider(c); err == nil && prov != nil && p.Kind == kindWeb {
			dctx, cancel := context.WithTimeout(ctx, 20*time.Second)
			taken, err := dns.Exists(dctx, prov, c.Zone, appHostname(c, p.Name))
			cancel()
			if err != nil {
				return http.StatusBadGateway, "vérification du DNS : " + err.Error()
			}
			if taken {
				return http.StatusConflict, appHostname(c, p.Name) + " existe déjà dans votre DNS (un autre site l'utilise) : choisissez un autre nom de projet"
			}
		}
	}
	return 0, ""
}

func (s *Server) handleCreateStack(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var in stackInput
	if !decodeJSON(w, r, &in) {
		return
	}
	parsed, msg, errs := in.parse(true)
	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	if errs != nil {
		writeStackErrors(w, errs)
		return
	}
	user := currentUser(r)
	if user.AppsSuspended != 0 {
		writeError(w, http.StatusForbidden, errSuspended)
		return
	}
	if in.NodeID != 0 && !isAdmin(user) {
		writeError(w, http.StatusForbidden, "seul un admin choisit la machine d'un projet")
		return
	}
	c, err := s.loadDNSConfig(ctx)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	want := planServices(in.Name, parsed)
	// The stack's own name too: no app nor stack may take it, even without an exposed service.
	names := append([]plannedApp{{Name: in.Name, Kind: kindService}}, want...)
	if slices.ContainsFunc(want, func(p plannedApp) bool { return p.Name == in.Name }) {
		names = want
	}
	if code, msg := s.checkStackNames(ctx, r, c, names, nil); code != 0 {
		writeError(w, code, msg)
		return
	}
	node, err := s.pickNode(ctx, in.NodeID)
	if errors.Is(err, errNoNode) {
		writeError(w, http.StatusServiceUnavailable, "aucun node en ligne pour accueillir le projet")
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
	sealed, err := s.sealStackEnv(in.Env)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	now := time.Now().Unix()
	var st db.Stack
	var created []db.App
	err = s.store.InTx(ctx, func(q *db.Queries) error {
		var err error
		st, err = q.CreateStack(ctx, db.CreateStackParams{Name: in.Name, OwnerID: user.ID, NodeID: node.ID, Compose: in.Compose,
			EnvSealed: sealed, CreatedAt: now, UpdatedAt: now})
		if err != nil {
			return err
		}
		for _, p := range want {
			a, err := s.createStackApp(ctx, q, st, p)
			if err != nil {
				return err
			}
			created = append(created, a)
		}
		return nil
	})
	if err != nil && strings.Contains(err.Error(), "UNIQUE") {
		writeError(w, http.StatusConflict, "ce nom est déjà pris : choisissez-en un autre")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	for _, a := range created {
		if a.Kind == kindWeb {
			if err := s.createAppRecord(ctx, c, a, node); err != nil {
				s.removeStack(ctx, st, true)
				writeError(w, http.StatusBadGateway, err.Error())
				return
			}
		}
		s.appEvent(a.ID, eventInfo, "Créée par "+user.DisplayName+" avec le projet "+st.Name+" sur le node "+node.Name)
	}
	s.logger.Info("stack created", "stack", st.Name, "services", len(created), "node", node.Name, "by", user.DisplayName)
	s.push(ctx, node.ID)
	s.writeStack(w, r, http.StatusCreated, st)
}

func (s *Server) createStackApp(ctx context.Context, q *db.Queries, st db.Stack, p plannedApp) (db.App, error) {
	sealed, err := s.sealEnv(p.Env)
	if err != nil {
		return db.App{}, err
	}
	now := time.Now().Unix()
	return q.CreateStackApp(ctx, db.CreateStackAppParams{Name: p.Name, OwnerID: st.OwnerID, NodeID: st.NodeID, Kind: p.Kind, Image: p.Image,
		Port: p.Port, EnvSealed: sealed, MemoryMb: p.MemoryMB, StackID: st.ID, Service: p.Service, ServiceSpec: p.Spec, CreatedAt: now, UpdatedAt: now})
}

// createAppRecord points an app's domain to its node, when Forgeyard manages the DNS records.
func (s *Server) createAppRecord(ctx context.Context, c dnsConfig, a db.App, node db.Node) error {
	p, err := s.dnsProvider(c)
	if err != nil {
		return errors.New("DNS : " + err.Error())
	}
	host := webHost(c, a)
	if p == nil || host == "" {
		return nil
	}
	if err := s.setAppRecord(ctx, c, p, host, nodeIP(node, c)); err != nil {
		return errors.New("création de l'enregistrement DNS " + host + " : " + err.Error())
	}
	if err := s.store.SetAppDNSName(ctx, db.SetAppDNSNameParams{DnsName: host, ID: a.ID}); err != nil {
		return err
	}
	s.appEvent(a.ID, eventInfo, "DNS créé : "+host)
	return nil
}

// handleUpdateStack saves a new compose file or new variables: only the services that changed are
// deployed again, the others keep running untouched.
func (s *Server) handleUpdateStack(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	st, ok := s.stackFromPath(w, r)
	if !ok {
		return
	}
	var in stackInput
	if !decodeJSON(w, r, &in) {
		return
	}
	in.Name = st.Name
	parsed, msg, errs := in.parse(false)
	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	if errs != nil {
		writeStackErrors(w, errs)
		return
	}
	apps, err := s.store.ListStackApps(ctx, st.ID)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	var existing []stackApp
	own := map[string]bool{}
	for _, a := range apps {
		env, err := s.openEnv(a.EnvSealed)
		if err != nil {
			s.internalError(w, r, err)
			return
		}
		existing = append(existing, stackApp{App: a, Env: env})
		own[a.Name] = true
	}
	own[st.Name] = true // the stack holds its own name
	plan := diffStack(existing, planServices(st.Name, parsed))
	c, err := s.loadDNSConfig(ctx)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	// A replaced service frees its name.
	for _, a := range plan.Remove {
		delete(own, a.Name)
	}
	own[st.Name] = true
	if code, msg := s.checkStackNames(ctx, r, c, plan.Create, own); code != 0 {
		writeError(w, code, msg)
		return
	}
	sealed, err := s.sealStackEnv(in.Env)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	node, err := s.store.GetNode(ctx, st.NodeID)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	user := currentUser(r)
	for _, a := range plan.Remove {
		if err := s.removeApp(ctx, a); err != nil {
			s.internalError(w, r, err)
			return
		}
	}
	var created []db.App
	err = s.store.InTx(ctx, func(q *db.Queries) error {
		var err error
		if st, err = q.UpdateStack(ctx, db.UpdateStackParams{Compose: in.Compose, EnvSealed: sealed, UpdatedAt: time.Now().Unix(), ID: st.ID}); err != nil {
			return err
		}
		for _, u := range plan.Update {
			env, err := s.sealEnv(u.Want.Env)
			if err != nil {
				return err
			}
			if _, err := q.UpdateStackApp(ctx, db.UpdateStackAppParams{Image: u.Want.Image, Port: u.Want.Port, EnvSealed: env,
				MemoryMb: u.Want.MemoryMB, ServiceSpec: u.Want.Spec, UpdatedAt: time.Now().Unix(), ID: u.App.ID}); err != nil {
				return err
			}
		}
		for _, p := range plan.Create {
			a, err := s.createStackApp(ctx, q, st, p)
			if err != nil {
				return err
			}
			created = append(created, a)
		}
		return nil
	})
	if err != nil && strings.Contains(err.Error(), "UNIQUE") {
		writeError(w, http.StatusConflict, "un service prendrait le nom d'une app existante : renommez-le")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	for _, a := range created {
		if a.Kind == kindWeb {
			if err := s.createAppRecord(ctx, c, a, node); err != nil {
				s.logger.Warn("creating a stack service's DNS record failed", "app", a.Name, "err", err)
				s.appEvent(a.ID, eventError, err.Error())
			}
		}
		s.appEvent(a.ID, eventInfo, "Ajoutée au projet "+st.Name+" par "+user.DisplayName)
	}
	for _, u := range plan.Update {
		s.appEvent(u.App.ID, eventInfo, "Configuration modifiée par "+user.DisplayName+" (compose du projet)")
	}
	s.logger.Info("stack updated", "stack", st.Name, "created", len(plan.Create), "updated", len(plan.Update), "removed", len(plan.Remove), "by", user.DisplayName)
	s.push(ctx, st.NodeID)
	s.writeStack(w, r, http.StatusOK, st)
}

// handleStackAction starts, stops or redeploys every service of a stack. Starting and redeploying make new
// containers (images pulled again) and run the jobs again.
func (s *Server) handleStackAction(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	st, ok := s.stackFromPath(w, r)
	if !ok {
		return
	}
	action := r.PathValue("action")
	params := db.SetStackRunningParams{UpdatedAt: time.Now().Unix(), StackID: st.ID}
	switch action {
	case "start", "redeploy":
		owner, err := s.store.GetUserByID(ctx, st.OwnerID)
		if err != nil {
			s.internalError(w, r, err)
			return
		}
		if owner.AppsSuspended != 0 {
			writeError(w, http.StatusForbidden, errSuspended)
			return
		}
		params.Running, params.Generation = 1, 1
	case "stop":
		params.Running, params.Generation = 0, 0
	default:
		writeError(w, http.StatusNotFound, "action inconnue")
		return
	}
	if err := s.store.SetStackRunning(ctx, params); err != nil {
		s.internalError(w, r, err)
		return
	}
	apps, err := s.store.ListStackApps(ctx, st.ID)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	verb := map[string]string{"start": "Démarrée", "stop": "Arrêtée", "redeploy": "Redéployée"}[action]
	for _, a := range apps {
		if params.Running == 1 {
			if err := s.store.ClearCrashSuspension(ctx, a.ID); err != nil {
				s.internalError(w, r, err)
				return
			}
			s.mu.Lock()
			delete(s.crashes, a.ID)
			s.mu.Unlock()
		}
		s.appEvent(a.ID, eventInfo, verb+" avec le projet par "+currentUser(r).DisplayName)
	}
	s.logger.Info("stack "+action, "stack", st.Name, "by", currentUser(r).DisplayName)
	s.push(ctx, st.NodeID)
	s.writeStack(w, r, http.StatusOK, st)
}

// handleDeleteStack deletes a stack, its apps and its network; its volumes only when ?volumes=1 confirms.
func (s *Server) handleDeleteStack(w http.ResponseWriter, r *http.Request) {
	st, ok := s.stackFromPath(w, r)
	if !ok {
		return
	}
	volumes := r.URL.Query().Get("volumes") == "1"
	if err := s.removeStack(r.Context(), st, volumes); err != nil {
		s.internalError(w, r, err)
		return
	}
	s.logger.Info("stack deleted", "stack", st.Name, "volumes", volumes, "by", currentUser(r).DisplayName)
	w.WriteHeader(http.StatusNoContent)
}

// removeStack deletes a stack's apps (their DNS records and containers), its backup policies and the
// stack; its node then deletes the network, and the volumes when asked.
func (s *Server) removeStack(ctx context.Context, st db.Stack, volumes bool) error {
	apps, err := s.store.ListStackApps(ctx, st.ID)
	if err != nil {
		return err
	}
	for _, a := range apps {
		if err := s.removeApp(ctx, a); err != nil {
			return err
		}
	}
	if err := s.store.DeleteStackBackupPolicies(ctx, st.ID); err != nil {
		return err
	}
	if err := s.store.DeleteStack(ctx, st.ID); err != nil {
		return err
	}
	s.nodes.DropStack(st.NodeID, st.ID, volumes)
	return nil
}

// errStackApp refuses to change one service of a stack on its own: the compose file says how it runs.
const errStackApp = "ce service appartient à un projet : modifiez son docker-compose.yml"
