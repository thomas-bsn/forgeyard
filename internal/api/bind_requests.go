package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/thomas-bsn/forgeyard/internal/store/db"
)

// bindMount names a specific access on the node's host filesystem. A missing mode is read-only.
type bindMount struct {
	Source   string `json:"source"`
	Target   string `json:"target"`
	ReadOnly *bool  `json:"readOnly"`
	Service  string `json:"service,omitempty"`
}

type bindRequestInput struct {
	appInput
	BindMounts []bindMount `json:"bindMounts"`
	LogoImage  string      `json:"logoImage,omitempty"`
	Stack      *stackInput `json:"stack,omitempty"`
	Change     *bindChange `json:"change,omitempty"`
}

// bindChange freezes the intended authorization and placement for an existing resource.
// The current resource remains untouched until a superadmin approves the request.
type bindChange struct {
	ResourceID   int64       `json:"resourceId"`
	ResourceType string      `json:"resourceType"` // app or stack
	NodeID       int64       `json:"nodeId"`
	BindMounts   []bindMount `json:"bindMounts"`
	Stack        *stackInput `json:"stack,omitempty"`
}

type bindRequestResponse struct {
	ID              int64            `json:"id"`
	AuthorID        int64            `json:"authorId"`
	AuthorName      string           `json:"authorName"`
	NodeName        string           `json:"nodeName"`
	Config          bindRequestInput `json:"config"`
	EnvKeys         []string         `json:"envKeys"`
	Status          string           `json:"status"`
	Reason          string           `json:"reason"`
	CreatedAt       int64            `json:"createdAt"`
	DecidedAt       int64            `json:"decidedAt"`
	AppID           int64            `json:"appId,omitempty"`
	StackID         int64            `json:"stackId,omitempty"`
	DeploymentState string           `json:"deploymentState,omitempty"`
	DeploymentError string           `json:"deploymentError,omitempty"`
}

func (s *Server) handleCreateBindChange(w http.ResponseWriter, r *http.Request, in *bindRequestInput) {
	change := in.Change
	if change.ResourceID <= 0 || (change.ResourceType != "app" && change.ResourceType != "stack") {
		writeError(w, http.StatusBadRequest, "ressource de bind mount invalide")
		return
	}
	user := currentUser(r)
	if user.AppsSuspended != 0 {
		writeError(w, http.StatusForbidden, errSuspended)
		return
	}
	var oldMounts []bindMount
	var currentNode int64
	var oldStackEnv map[string]string
	var appEnv map[string]string
	var originalMountsJSON string
	var originalStackBindings string
	var authorizationState string
	var app db.App
	var stack db.Stack
	if change.ResourceType == "app" {
		var err error
		app, err = s.store.GetApp(r.Context(), change.ResourceID)
		if err != nil || (app.OwnerID != user.ID && user.Role != "admin" && user.Role != "superadmin") || app.StackID != 0 {
			writeError(w, http.StatusNotFound, "app introuvable")
			return
		}
		if len(appBinds(app)) == 0 {
			writeError(w, http.StatusConflict, "l'app ne possède aucun bind mount à modifier")
			return
		}
		if app.MovingFrom != 0 {
			writeError(w, http.StatusConflict, "terminez le déplacement de l'app avant de demander un changement d'accès")
			return
		}
		if change.NodeID > 0 && change.NodeID != app.NodeID && app.Running != 0 {
			writeError(w, http.StatusConflict, "arrêtez l'app avant de demander un changement de node")
			return
		}
		authorizationState = strconv.FormatInt(app.BindAuthorized, 10)
		currentNode = app.NodeID
		originalMountsJSON = app.BindMounts
		oldMounts = bindAPI(appBinds(app))
		in.Name, in.Kind, in.Image, in.Dockerfile, in.Port, in.MemoryMB = app.Name, app.Kind, app.Image, app.Dockerfile, int(app.Port), int(app.MemoryMb)
		in.Volumes = nil
		_ = json.Unmarshal([]byte(app.Volumes), &in.Volumes)
		in.GPU = appGpu(app)
		appEnv, err = s.openEnv(app.EnvSealed)
		if err != nil {
			s.internalError(w, r, err)
			return
		}
		in.Env = appEnv
	} else {
		var err error
		stack, err = s.store.GetStack(r.Context(), change.ResourceID)
		if err != nil || (stack.OwnerID != user.ID && user.Role != "admin" && user.Role != "superadmin") {
			writeError(w, http.StatusNotFound, "projet introuvable")
			return
		}
		apps, e := s.store.ListStackApps(r.Context(), stack.ID)
		if e != nil {
			s.internalError(w, r, e)
			return
		}
		sort.Slice(apps, func(i, j int) bool { return apps[i].Service < apps[j].Service })
		for _, a := range apps {
			oldMounts = append(oldMounts, bindAPI(appBinds(a))...)
			originalStackBindings += a.Service + ":" + a.BindMounts + "\n"
			authorizationState += a.Service + ":" + strconv.FormatInt(a.BindAuthorized, 10) + "\n"
		}
		if change.NodeID > 0 && change.NodeID != stack.NodeID {
			for _, a := range apps {
				if a.Running != 0 {
					writeError(w, http.StatusConflict, "arrêtez tous les services du projet avant de demander un changement de node")
					return
				}
			}
		}
		if len(apps) == 0 {
			writeError(w, http.StatusConflict, "le projet ne contient plus de services")
			return
		}
		currentNode = stack.NodeID
		originalMountsJSON = ""
		oldStackEnv, err = s.openStackEnv(stack.EnvSealed)
		if err != nil {
			s.internalError(w, r, err)
			return
		}
	}
	if change.NodeID <= 0 {
		writeError(w, http.StatusBadRequest, "choisissez le node cible")
		return
	}
	node, err := s.store.GetNode(r.Context(), change.NodeID)
	if err != nil || node.Status != "active" {
		writeError(w, http.StatusBadRequest, "le node cible n'accepte pas de déploiements")
		return
	}
	if change.ResourceType == "stack" {
		if change.Stack == nil {
			writeError(w, http.StatusBadRequest, "Compose cible requis")
			return
		}
		change.Stack.Name, change.Stack.NodeID = stack.Name, change.NodeID
		in.Stack = change.Stack
		if in.Stack.Env == nil {
			in.Stack.Env = oldStackEnv
		}
		reg, err := s.openRegistry(stack.RegistrySealed)
		if err != nil {
			s.internalError(w, r, err)
			return
		}
		if change.Stack.Registry == nil || change.Stack.Registry.Password == "" {
			in.Stack.Registry = &reg
		} else {
			reg.Password = change.Stack.Registry.Password
			in.Stack.Registry = &reg
		}
		in.NodeID = change.NodeID
		parsed, msg, errs := in.Stack.parse(true)
		if msg != "" {
			s.logger.Warn("bind change Compose parse failed", "stack", change.ResourceID, "message", msg)
			writeError(w, http.StatusBadRequest, msg)
			return
		}
		if errs != nil {
			writeStackErrors(w, errs)
			return
		}
		in.BindMounts = nil
		for _, svc := range parsed.Services {
			targets := map[string]bool{}
			for _, mount := range svc.Volumes {
				if mount.Kind != "bind" {
					targets[mount.Target] = true
				}
			}
			for _, mount := range svc.Volumes {
				if mount.Kind != "bind" {
					continue
				}
				if targets[mount.Target] {
					writeError(w, http.StatusBadRequest, "destination de montage en double pour le service "+svc.Name)
					return
				}
				targets[mount.Target] = true
				ro := mount.ReadOnly
				in.BindMounts = append(in.BindMounts, bindMount{Source: mount.Source, Target: mount.Target, ReadOnly: &ro, Service: svc.Name})
			}
		}
		if len(in.BindMounts) == 0 {
			writeError(w, http.StatusBadRequest, "le Compose soumis doit conserver au moins un bind mount")
			return
		}
	} else {
		in.NodeID = app.NodeID
		if msg := in.appInput.validate(false); msg != "" {
			writeError(w, http.StatusBadRequest, msg)
			return
		}
		in.BindMounts = change.BindMounts
		in.NodeID = change.NodeID
		if len(in.BindMounts) > 10 {
			writeError(w, http.StatusBadRequest, "indiquez au plus 10 bind mounts")
			return
		}
		for i := range in.BindMounts {
			m := &in.BindMounts[i]
			m.Source, m.Target = strings.TrimSpace(m.Source), strings.TrimSpace(m.Target)
			if !strings.HasPrefix(m.Source, "/") || !strings.HasPrefix(m.Target, "/") || strings.ContainsAny(m.Source+m.Target, "\x00\n\r") {
				writeError(w, http.StatusBadRequest, "les sources et destinations doivent être absolues")
				return
			}
			m.Source, m.Target = path.Clean(m.Source), path.Clean(m.Target)
			if m.Target == "/" || m.Target == "/proc" || m.Target == "/sys" || m.Target == "/dev" || strings.HasPrefix(m.Target, "/proc/") || strings.HasPrefix(m.Target, "/sys/") || strings.HasPrefix(m.Target, "/dev/") {
				writeError(w, http.StatusBadRequest, "destination de montage interdite : "+m.Target)
				return
			}
			for _, named := range in.Volumes {
				if named == m.Target || strings.HasPrefix(named, m.Target+"/") || strings.HasPrefix(m.Target, named+"/") {
					writeError(w, http.StatusBadRequest, "destination en conflit avec un volume nommé : "+m.Target)
					return
				}
			}
			for j := 0; j < i; j++ {
				old := in.BindMounts[j]
				if old.Target == m.Target || strings.HasPrefix(old.Target, m.Target+"/") || strings.HasPrefix(m.Target, old.Target+"/") {
					writeError(w, http.StatusBadRequest, "destinations de bind mount en conflit")
					return
				}
			}
			if m.ReadOnly == nil {
				ro := true
				m.ReadOnly = &ro
			}
		}
		if len(in.BindMounts) == 0 {
			writeError(w, http.StatusBadRequest, "indiquez les montages à autoriser sur le node cible")
			return
		}
	}
	// Bind request config contains only the proposed access delta and a stable resource link;
	// ordinary app code/Compose values remain editable while the request is pending.
	if change.ResourceType == "stack" {
		change.Stack.Env = nil
		if change.Stack.Registry != nil {
			change.Stack.Registry.Password = ""
		}
	} else {
		in.Env = nil
	}
	config, err := json.Marshal(in)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	secretEnv := map[string]string{}
	if change.ResourceType == "stack" {
		secretEnv = oldStackEnv
		if secretEnv == nil {
			secretEnv = map[string]string{}
		}
		reg, e := s.openRegistry(stack.RegistrySealed)
		if e != nil {
			s.internalError(w, r, e)
			return
		}
		if in.Stack.Registry != nil && in.Stack.Registry.Password != "" {
			reg.Password = in.Stack.Registry.Password
		}
		secretEnv["__FORGEYARD_STACK_REGISTRY"] = string(must(json.Marshal(reg)))
	} else {
		secretEnv = appEnv
	}
	if change.ResourceType == "stack" {
		originalMountsJSON = originalStackBindings
	}
	secretEnv["__FORGEYARD_BIND_CHANGE_BASE"] = originalMountsJSON
	secretEnv["__FORGEYARD_BIND_CHANGE_NODE"] = strconv.FormatInt(currentNode, 10)
	secretEnv["__FORGEYARD_BIND_CHANGE_AUTH"] = authorizationState
	sealed, err := s.sealEnv(secretEnv)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	var row db.BindRequest
	err = s.store.InTx(r.Context(), func(q *db.Queries) error {
		pending, e := q.HasPendingBindChange(r.Context(), db.HasPendingBindChangeParams{ChangeResourceType: change.ResourceType, ChangeResourceID: change.ResourceID})
		if e != nil {
			return e
		}
		if pending != 0 {
			return errors.New("une demande de modification est déjà en attente pour cette ressource")
		}
		row, e = q.CreateBindRequest(r.Context(), db.CreateBindRequestParams{AuthorID: user.ID, NodeID: change.NodeID, Config: string(config), EnvSealed: sealed, CreatedAt: time.Now().Unix(), ChangeResourceType: change.ResourceType, ChangeResourceID: change.ResourceID})
		return e
	})
	if err != nil {
		if strings.Contains(err.Error(), "déjà en attente") {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		s.internalError(w, r, err)
		return
	}
	s.notifyBindRequest(row, bindRequestSubmitted)
	if user.Role == "superadmin" {
		_ = s.approveBindRequest(r, row)
		row, err = s.store.GetBindRequest(r.Context(), row.ID)
		if err != nil {
			s.internalError(w, r, err)
			return
		}
	}
	s.writeBindRequest(w, r, row, http.StatusCreated)
}

func (in *bindRequestInput) validate() string {
	if in.Stack != nil {
		if _, reserved := in.Stack.Env["__FORGEYARD_STACK_REGISTRY"]; reserved {
			return "nom de variable réservé"
		}
		in.Stack.NodeID = in.NodeID
		parsed, msg, errs := in.Stack.parse(true)
		if msg != "" {
			return msg
		}
		if errs != nil {
			return errs.Error()
		}
		if in.NodeID <= 0 {
			return "choisissez le node où se trouvent les fichiers"
		}
		in.BindMounts = nil
		for _, svc := range parsed.Services {
			targets := map[string]bool{}
			for _, mount := range svc.Volumes {
				if mount.Kind != "bind" {
					targets[mount.Target] = true
				}
			}
			for _, mount := range svc.Volumes {
				if mount.Kind == "bind" {
					if mount.Target == "/" || mount.Target == "/proc" || mount.Target == "/sys" || mount.Target == "/dev" || strings.HasPrefix(mount.Target, "/proc/") || strings.HasPrefix(mount.Target, "/sys/") || strings.HasPrefix(mount.Target, "/dev/") {
						return "destination impossible : " + mount.Target
					}
					if targets[mount.Target] {
						return "destination de montage en double pour le service " + svc.Name
					}
					targets[mount.Target] = true
					ro := mount.ReadOnly
					in.BindMounts = append(in.BindMounts, bindMount{Source: mount.Source, Target: mount.Target, ReadOnly: &ro, Service: svc.Name})
				}
			}
		}
		if len(in.BindMounts) == 0 || len(in.BindMounts) > 50 {
			return "indiquez entre 1 et 50 bind mounts dans le Compose"
		}
		services := map[string]bool{}
		for _, svc := range parsed.Services {
			services[svc.Name] = true
		}
		targets := map[string]bool{}
		for i := range in.BindMounts {
			m := &in.BindMounts[i]
			m.Source, m.Target, m.Service = strings.TrimSpace(m.Source), strings.TrimSpace(m.Target), strings.TrimSpace(m.Service)
			if !services[m.Service] || !strings.HasPrefix(m.Source, "/") || !strings.HasPrefix(m.Target, "/") || strings.ContainsAny(m.Source+m.Target, "\x00\n\r") {
				return "chaque bind mount Compose doit préciser des chemins absolus"
			}
			m.Source, m.Target = path.Clean(m.Source), path.Clean(m.Target)
			key := m.Service + "\x00" + m.Target
			if targets[key] {
				return "destination de montage en double pour le service " + m.Service
			}
			targets[key] = true
			if m.ReadOnly == nil {
				ro := true
				m.ReadOnly = &ro
			}
		}
		return ""
	}
	if msg := in.appInput.validate(true); msg != "" {
		return msg
	}
	if in.NodeID <= 0 {
		return "choisissez le node où se trouvent les fichiers"
	}
	if len(in.BindMounts) == 0 || len(in.BindMounts) > 10 {
		return "indiquez entre 1 et 10 bind mounts"
	}
	targets := map[string]bool{}
	for _, target := range in.Volumes {
		targets[target] = true
	}
	for i := range in.BindMounts {
		m := &in.BindMounts[i]
		m.Source = strings.TrimSpace(m.Source)
		m.Target = strings.TrimSpace(m.Target)
		if !strings.HasPrefix(m.Source, "/") || !strings.HasPrefix(m.Target, "/") || len(m.Source) > 4096 || len(m.Target) > 200 || strings.ContainsAny(m.Source+m.Target, "\x00\n\r") {
			return "les sources et destinations des bind mounts doivent être des chemins absolus"
		}
		m.Source = path.Clean(m.Source)
		m.Target = path.Clean(m.Target)
		if m.Target == "/" || m.Target == "/proc" || m.Target == "/sys" || m.Target == "/dev" || strings.HasPrefix(m.Target, "/proc/") || strings.HasPrefix(m.Target, "/sys/") || strings.HasPrefix(m.Target, "/dev/") {
			return "destination impossible : " + m.Target
		}
		if targets[m.Target] {
			return "destination de montage en double : " + m.Target
		}
		targets[m.Target] = true
		if m.ReadOnly == nil {
			ro := true
			m.ReadOnly = &ro
		}
	}
	if in.LogoImage != "" {
		if _, _, err := decodeDataURL(in.LogoImage, maxAvatarBytes); err != nil {
			return "logo invalide"
		}
	}
	return ""
}

// Only active nodes accept apps. Offline active nodes remain selectable for a request; approval will
// require a live agent to verify the paths, rather than silently choosing a different machine.
func (s *Server) handleBindNodeChoices(w http.ResponseWriter, r *http.Request) {
	nodes, err := s.store.ListNodes(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	type choice struct {
		ID     int64  `json:"id"`
		Name   string `json:"name"`
		Online bool   `json:"online"`
		Cpus   int64  `json:"cpus"`
		Apps   int64  `json:"apps"`
	}
	counts, err := s.store.CountAppsByNode(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	appCounts := map[int64]int64{}
	for _, count := range counts {
		appCounts[count.NodeID] = count.Count
	}
	out := []choice{}
	for _, n := range nodes {
		if n.Status == "active" {
			_, online := s.nodes.Live(n.ID)
			out = append(out, choice{n.ID, n.Name, online, n.Cpus, appCounts[n.ID]})
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleCreateBindRequest(w http.ResponseWriter, r *http.Request) {
	var in bindRequestInput
	if !decodeJSON(w, r, &in) {
		return
	}
	if in.Change != nil {
		s.handleCreateBindChange(w, r, &in)
		return
	}
	if msg := in.validate(); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	if currentUser(r).AppsSuspended != 0 {
		writeError(w, http.StatusForbidden, errSuspended)
		return
	}
	n, err := s.store.GetNode(r.Context(), in.NodeID)
	if err != nil || n.Status != "active" {
		writeError(w, http.StatusBadRequest, "ce node n'accepte pas de déploiements")
		return
	}
	secretEnv := in.Env
	if in.Stack != nil {
		secretEnv = in.Stack.Env
		if secretEnv == nil {
			secretEnv = map[string]string{}
		}
		if in.Stack.Registry != nil && in.Stack.Registry.Password != "" {
			encoded := string(must(json.Marshal(in.Stack.Registry)))
			secretEnv["__FORGEYARD_STACK_REGISTRY"] = encoded
		}
	}
	sealed, err := s.sealEnv(secretEnv)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	in.Env = nil
	if in.Stack != nil {
		in.Stack.Env = nil
		if in.Stack.Registry != nil {
			in.Stack.Registry.Password = ""
		}
	}
	config, err := json.Marshal(in)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	request, err := s.store.CreateBindRequest(r.Context(), db.CreateBindRequestParams{AuthorID: currentUser(r).ID, NodeID: in.NodeID, Config: string(config), EnvSealed: sealed, CreatedAt: time.Now().Unix()})
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.notifyBindRequest(request, bindRequestSubmitted)
	if currentUser(r).Role == "superadmin" {
		_ = s.approveBindRequest(r, request)
		request, err = s.store.GetBindRequest(r.Context(), request.ID)
		if err != nil {
			s.internalError(w, r, err)
			return
		}
	}
	s.writeBindRequest(w, r, request, http.StatusCreated)
}

func (s *Server) bindResponse(r *http.Request, row db.BindRequest) (bindRequestResponse, error) {
	out := bindRequestResponse{ID: row.ID, AuthorID: row.AuthorID, Status: row.Status, Reason: row.Reason, CreatedAt: row.CreatedAt, DecidedAt: row.DecidedAt, EnvKeys: []string{}}
	if row.AppID.Valid {
		out.AppID = row.AppID.Int64
		if state, ok := s.nodes.AppStatus(out.AppID); ok {
			out.DeploymentState = state.Status.GetState()
			out.DeploymentError = state.Status.GetError()
		} else {
			out.DeploymentState = "pending"
		}
	}
	if row.StackID.Valid {
		out.StackID = row.StackID.Int64
	}
	if err := json.Unmarshal([]byte(row.Config), &out.Config); err != nil {
		return out, err
	}
	if row.ChangeResourceType != "" {
		out.Config.Change = &bindChange{ResourceID: row.ChangeResourceID, ResourceType: row.ChangeResourceType, NodeID: row.NodeID, BindMounts: out.Config.BindMounts, Stack: out.Config.Stack}
	}
	// Never expose saved environment values in ordinary list or detail responses.
	out.Config.Env = nil
	if out.Config.Stack != nil {
		out.Config.Stack.Env = nil
		if out.Config.Change != nil && out.Config.Change.Stack != nil {
			out.Config.Change.Stack.Env = nil
		}
	}
	env, err := s.openEnv(row.EnvSealed)
	if err != nil {
		return out, err
	}
	for k := range env {
		if strings.HasPrefix(k, "__FORGEYARD_BIND_CHANGE_") {
			continue
		}
		if k == "__FORGEYARD_STACK_REGISTRY" {
			out.EnvKeys = append(out.EnvKeys, "registry.password")
		} else {
			out.EnvKeys = append(out.EnvKeys, k)
		}
	}
	sort.Strings(out.EnvKeys)
	if author, err := s.store.GetUserByID(r.Context(), row.AuthorID); err == nil {
		out.AuthorName = author.DisplayName
	}
	if node, err := s.store.GetNode(r.Context(), row.NodeID); err == nil {
		out.NodeName = node.Name
	}
	return out, nil
}

func (s *Server) writeBindRequest(w http.ResponseWriter, r *http.Request, row db.BindRequest, status int) {
	out, err := s.bindResponse(r, row)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, status, out)
}

func (s *Server) handleListBindRequests(w http.ResponseWriter, r *http.Request) {
	var rows []db.BindRequest
	var err error
	if strings.HasPrefix(r.URL.Path, "/api/admin/") {
		rows, err = s.store.ListBindRequests(r.Context())
	} else {
		rows, err = s.store.ListOwnBindRequests(r.Context(), currentUser(r).ID)
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	out := []bindRequestResponse{}
	for _, row := range rows {
		resp, err := s.bindResponse(r, row)
		if err != nil {
			s.internalError(w, r, err)
			return
		}
		out = append(out, resp)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) loadBindRequest(w http.ResponseWriter, r *http.Request) (db.BindRequest, bool) {
	id, parseErr := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if parseErr != nil {
		writeError(w, http.StatusNotFound, "demande introuvable")
		return db.BindRequest{}, false
	}
	row, err := s.store.GetBindRequest(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && row.AuthorID != currentUser(r).ID && currentUser(r).Role != "superadmin") {
		writeError(w, http.StatusNotFound, "demande introuvable")
		return db.BindRequest{}, false
	}
	if err != nil {
		s.internalError(w, r, err)
		return db.BindRequest{}, false
	}
	return row, true
}

func (s *Server) handleGetBindRequest(w http.ResponseWriter, r *http.Request) {
	row, ok := s.loadBindRequest(w, r)
	if !ok {
		return
	}
	s.writeBindRequest(w, r, row, http.StatusOK)
}

func (s *Server) handleRevealBindRequest(w http.ResponseWriter, r *http.Request) {
	row, ok := s.loadBindRequest(w, r)
	if !ok {
		return
	}
	env, err := s.openEnv(row.EnvSealed)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if raw := env["__FORGEYARD_STACK_REGISTRY"]; raw != "" {
		delete(env, "__FORGEYARD_STACK_REGISTRY")
		var registry registryInput
		if err := json.Unmarshal([]byte(raw), &registry); err != nil {
			s.internalError(w, r, err)
			return
		}
		env["registry.password"] = registry.Password
	}
	delete(env, "__FORGEYARD_BIND_CHANGE_BASE")
	delete(env, "__FORGEYARD_BIND_CHANGE_NODE")
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, env)
}

func (s *Server) handleDecideBindRequest(w http.ResponseWriter, r *http.Request) {
	row, ok := s.loadBindRequest(w, r)
	if !ok {
		return
	}
	status, reason := "cancelled", ""
	if strings.HasSuffix(r.URL.Path, "/refuse") {
		var in struct {
			Reason string `json:"reason"`
		}
		if !decodeJSON(w, r, &in) {
			return
		}
		reason = strings.TrimSpace(in.Reason)
		if reason == "" || len(reason) > 2000 {
			writeError(w, http.StatusBadRequest, "un motif de refus est obligatoire (2000 caractères maximum)")
			return
		}
		status = "refused"
	} else if row.AuthorID != currentUser(r).ID {
		writeError(w, http.StatusForbidden, "seul l'auteur peut annuler sa demande")
		return
	}
	var changed int64
	var err error
	if status == "cancelled" && row.Status == "failed" && row.ChangeResourceType != "" {
		changed, err = s.store.CancelFailedBindChange(r.Context(), db.CancelFailedBindChangeParams{DecidedAt: time.Now().Unix(), DecidedBy: sql.NullInt64{Int64: currentUser(r).ID, Valid: true}, ID: row.ID, AuthorID: currentUser(r).ID})
	} else {
		changed, err = s.store.DecideBindRequest(r.Context(), db.DecideBindRequestParams{Status: status, Reason: reason, DecidedAt: time.Now().Unix(), DecidedBy: sql.NullInt64{Int64: currentUser(r).ID, Valid: true}, ID: row.ID})
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if changed == 0 {
		writeError(w, http.StatusConflict, "cette demande n'est plus annulable")
		return
	}
	row, err = s.store.GetBindRequest(r.Context(), row.ID)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if row.Status == "refused" {
		s.notifyBindRequest(row, bindRequestRefused)
	} else {
		s.notifyBindRequest(row, bindRequestCancelled)
	}
	s.writeBindRequest(w, r, row, http.StatusOK)
}
