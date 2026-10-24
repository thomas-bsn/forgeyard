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
	// Never expose saved environment values in ordinary list or detail responses.
	out.Config.Env = nil
	if out.Config.Stack != nil {
		out.Config.Stack.Env = nil
	}
	env, err := s.openEnv(row.EnvSealed)
	if err != nil {
		return out, err
	}
	for k := range env {
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
	changed, err := s.store.DecideBindRequest(r.Context(), db.DecideBindRequestParams{Status: status, Reason: reason, DecidedAt: time.Now().Unix(), DecidedBy: sql.NullInt64{Int64: currentUser(r).ID, Valid: true}, ID: row.ID})
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if changed == 0 {
		writeError(w, http.StatusConflict, "cette demande n'est plus en attente")
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
