package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/thomas-bsn/forgeyard/internal/agentpb"
	"github.com/thomas-bsn/forgeyard/internal/compose"
	"github.com/thomas-bsn/forgeyard/internal/store/db"
)

func (s *Server) bindChangeOwner(ctx context.Context, change *bindChange) (db.User, error) {
	if change.ResourceType == "app" {
		app, err := s.store.GetApp(ctx, change.ResourceID)
		if err != nil {
			return db.User{}, err
		}
		return s.store.GetUserByID(ctx, app.OwnerID)
	}
	if change.ResourceType == "stack" {
		stack, err := s.store.GetStack(ctx, change.ResourceID)
		if err != nil {
			return db.User{}, err
		}
		return s.store.GetUserByID(ctx, stack.OwnerID)
	}
	return db.User{}, errors.New("type de ressource inconnu")
}

func (s *Server) approveBindChange(ctx context.Context, r *http.Request, row db.BindRequest, owner db.User, in *bindRequestInput) error {
	change := in.Change
	node, err := s.pickNode(ctx, change.NodeID)
	if err != nil {
		return errors.New("le node cible doit être en ligne pour vérifier les chemins")
	}
	var mounts []bindMount
	var app db.App
	var stack db.Stack
	var services []db.App
	requestSecrets, err := s.openEnv(row.EnvSealed)
	if err != nil {
		return err
	}
	baseConfig := requestSecrets["__FORGEYARD_BIND_CHANGE_BASE"]
	baseNode, _ := strconv.ParseInt(requestSecrets["__FORGEYARD_BIND_CHANGE_NODE"], 10, 64)
	delete(requestSecrets, "__FORGEYARD_BIND_CHANGE_BASE")
	delete(requestSecrets, "__FORGEYARD_BIND_CHANGE_NODE")
	if change.ResourceType == "app" {
		app, err = s.store.GetApp(ctx, change.ResourceID)
		if err != nil {
			return err
		}
		if app.StackID != 0 {
			return errors.New("l'app a changé depuis la soumission ; soumettez une nouvelle demande")
		}
		if app.OwnerID != owner.ID {
			return errors.New("le propriétaire de l'app a changé depuis cette demande")
		}
		if change.ResourceType == "app" && change.NodeID != app.NodeID && app.MovingFrom != 0 {
			return errors.New("l'app est déjà en déplacement")
		}
		if app.BindMounts != baseConfig || app.NodeID != baseNode {
			return errors.New("les accès de l'app ont changé depuis cette demande")
		}
		if app.BindAuthorized == 0 && change.NodeID == app.NodeID {
			return errors.New("l'autorisation a été révoquée ; choisissez un node cible et des montages à réautoriser explicitement")
		}
		if change.NodeID != app.NodeID && app.Running != 0 {
			return errors.New("arrêtez l'app avant de changer de node")
		}
		mounts = in.BindMounts
	} else if change.ResourceType == "stack" {
		stack, err = s.store.GetStack(ctx, change.ResourceID)
		if err != nil {
			return err
		}
		if stack.OwnerID != owner.ID {
			return errors.New("le propriétaire du projet a changé depuis cette demande")
		}
		if stack.NodeID != baseNode {
			return errors.New("le node du projet a changé depuis cette demande")
		}
		services, err = s.store.ListStackApps(ctx, stack.ID)
		if err != nil {
			return err
		}
		sort.Slice(services, func(i, j int) bool { return services[i].Service < services[j].Service })
		var currentBindings string
		for _, service := range services {
			currentBindings += service.Service + ":" + service.BindMounts + "\n"
		}
		if currentBindings != baseConfig || stack.NodeID != baseNode {
			return errors.New("les accès du projet ont changé depuis cette demande")
		}
		for _, service := range services {
			if service.BindAuthorized == 0 && len(appBinds(service)) > 0 && change.NodeID == stack.NodeID {
				return errors.New("l'autorisation du projet a été révoquée ; choisissez un autre node et redemandez explicitement les accès")
			}
			if change.NodeID != stack.NodeID && service.Running != 0 {
				return errors.New("arrêtez tous les services du projet avant de changer de node")
			}
		}
		mounts = in.BindMounts
		in.Stack.Env, err = s.openStackEnv(stack.EnvSealed)
		if err != nil {
			return err
		}
		reg := registryInput{}
		if in.Stack.Registry != nil {
			reg = *in.Stack.Registry
		}
		if raw := in.Stack.Env["__FORGEYARD_STACK_REGISTRY"]; raw != "" {
			var saved registryInput
			if err := json.Unmarshal([]byte(raw), &saved); err != nil {
				return err
			}
			if in.Stack.Registry == nil || in.Stack.Registry.Password == "" {
				reg = saved
			} else {
				saved.Password = reg.Password
				reg = saved
			}
			delete(in.Stack.Env, "__FORGEYARD_STACK_REGISTRY")
		}
		if reg.Server != "" {
			in.Stack.Registry = &reg
		}
	} else {
		return errors.New("type de ressource inconnu")
	}
	check, err := s.nodes.ValidateBindMounts(ctx, node.ID, bindSpecs(mounts), s.protectedBindPaths(node)...)
	if err != nil {
		return err
	}
	if check.GetError() != "" {
		return errors.New(check.GetError())
	}
	if len(check.GetMounts()) != len(mounts) {
		return errors.New("réponse de vérification des montages incomplète")
	}
	for i, m := range check.GetMounts() {
		if m.Target != mounts[i].Target || m.ReadOnly != (mounts[i].ReadOnly == nil || *mounts[i].ReadOnly) || !strings.HasPrefix(m.Source, "/") {
			return errors.New("réponse de vérification des montages invalide")
		}
	}
	canonicalMounts := make([]bindMount, 0, len(check.GetMounts()))
	for i, m := range check.GetMounts() {
		ro := m.ReadOnly
		canonicalMounts = append(canonicalMounts, bindMount{Source: m.Source, Target: m.Target, ReadOnly: &ro, Service: mounts[i].Service})
	}
	canonical, _ := json.Marshal(check.GetMounts())
	var newNodeID int64
	if change.ResourceType == "app" {
		newNodeID = change.NodeID
		oldNode := app.NodeID
		if len(check.GetMounts()) == 0 {
			return errors.New("une app doit conserver au moins un bind mount autorisé")
		}
		err = s.store.InTx(ctx, func(q *db.Queries) error {
			running := int64(0)
			var changed int64
			if app.BindAuthorized == 0 {
				changed, err = q.ApplyBindChangeAppAfterRevocation(ctx, db.ApplyBindChangeAppAfterRevocationParams{BindMounts: string(canonical), NodeID: newNodeID, Running: running, UpdatedAt: time.Now().Unix(), ID: app.ID, BindMounts_2: app.BindMounts, NodeID_2: app.NodeID})
			} else {
				changed, err = q.ApplyBindChangeApp(ctx, db.ApplyBindChangeAppParams{BindMounts: string(canonical), NodeID: newNodeID, Running: running, UpdatedAt: time.Now().Unix(), ID: app.ID, BindMounts_2: app.BindMounts, NodeID_2: app.NodeID})
			}
			if err != nil {
				return err
			}
			if changed != 1 {
				return errors.New("l'app a changé ou son autorisation a été révoquée depuis la soumission")
			}
			n, e := q.CompleteBindRequest(ctx, row.ID)
			if e != nil {
				return e
			}
			if n != 1 {
				return errors.New("la demande a changé pendant le traitement")
			}
			return nil
		})
		if err != nil {
			return err
		}
		app.NodeID = newNodeID
		if newNodeID != oldNode {
			s.nodes.ResetApp(app.ID)
			s.push(ctx, oldNode)
		}
	} else {
		newNodeID = change.NodeID
		oldNode := stack.NodeID
		storedEnv, e := s.openStackEnv(stack.EnvSealed)
		if e != nil {
			return e
		}
		proposedInput := *in.Stack
		proposedInput.Env = storedEnv
		if _, msg, errs := proposedInput.parse(false); msg != "" {
			return errors.New(msg)
		} else if errs != nil {
			return errs
		}
		byService := map[string][]*agentpb.BindMount{}
		for i, m := range check.GetMounts() {
			byService[mounts[i].Service] = append(byService[mounts[i].Service], m)
		}
		if len(services) == 0 {
			return errors.New("le projet ne contient plus de services")
		}
		approvedMounts := make([]compose.BindMountChange, 0, len(canonicalMounts))
		for _, mount := range canonicalMounts {
			approvedMounts = append(approvedMounts, compose.BindMountChange{Service: mount.Service, Source: mount.Source, Target: mount.Target, ReadOnly: mount.ReadOnly == nil || *mount.ReadOnly})
		}
		mergedCompose, e := compose.MergeBindMounts(stack.Compose, proposedInput.Compose, approvedMounts)
		if e != nil {
			return e
		}
		mergedInput := stackInput{Name: stack.Name, Compose: mergedCompose, Env: storedEnv}
		merged, msg, errs := mergedInput.parse(false)
		if msg != "" {
			return errors.New(msg)
		}
		if errs != nil {
			return errs
		}
		err = s.store.InTx(ctx, func(q *db.Queries) error {
			for _, service := range services {
				var svc compose.Service
				for _, wanted := range merged.Services {
					if wanted.Name == service.Service {
						svc = wanted
						break
					}
				}
				raw, e := json.Marshal(svc)
				if e != nil {
					return e
				}
				binds := []*agentpb.BindMount{}
				for _, m := range byService[service.Service] {
					binds = append(binds, m)
				}
				var changed int64
				if service.BindAuthorized == 0 && len(appBinds(service)) > 0 {
					changed, e = q.ApplyBindChangeStackServiceAfterRevocation(ctx, db.ApplyBindChangeStackServiceAfterRevocationParams{BindMounts: string(must(json.Marshal(binds))), NodeID: newNodeID, UpdatedAt: time.Now().Unix(), ID: service.ID, BindMounts_2: service.BindMounts, NodeID_2: service.NodeID})
				} else if service.BindAuthorized == 0 {
					changed, e = q.ApplyBindChangeStackServiceWithoutAuthorization(ctx, db.ApplyBindChangeStackServiceWithoutAuthorizationParams{BindMounts: string(must(json.Marshal(binds))), NodeID: newNodeID, UpdatedAt: time.Now().Unix(), ID: service.ID, BindMounts_2: service.BindMounts, NodeID_2: service.NodeID})
				} else {
					changed, e = q.ApplyBindChangeStackService(ctx, db.ApplyBindChangeStackServiceParams{BindMounts: string(must(json.Marshal(binds))), NodeID: newNodeID, UpdatedAt: time.Now().Unix(), ID: service.ID, BindMounts_2: service.BindMounts, NodeID_2: service.NodeID})
				}
				if e != nil {
					return e
				}
				if changed != 1 {
					return errors.New("les accès du projet ont changé ou été révoqués depuis la soumission")
				}
				if e = q.UpdateStackAppPreserveRunning(ctx, db.UpdateStackAppPreserveRunningParams{Image: svc.Image, Port: int64(svc.Expose), EnvSealed: service.EnvSealed, MemoryMb: int64(svc.MemoryMB), ServiceSpec: string(raw), UpdatedAt: time.Now().Unix(), ID: service.ID}); e != nil {
					return e
				}
			}
			changed, e := q.ApplyBindChangeStack(ctx, db.ApplyBindChangeStackParams{Compose: mergedInput.Compose, NodeID: newNodeID, UpdatedAt: time.Now().Unix(), ID: stack.ID, NodeID_2: oldNode, Compose_2: stack.Compose, EnvSealed: stack.EnvSealed, RegistrySealed: stack.RegistrySealed})
			if e != nil {
				return e
			}
			if changed != 1 {
				return errors.New("le node du projet a changé depuis la soumission")
			}
			n, e := q.CompleteBindRequest(ctx, row.ID)
			if e != nil {
				return e
			}
			if n != 1 {
				return errors.New("la demande a changé pendant le traitement")
			}
			return nil
		})
		if err != nil {
			return err
		}
		if newNodeID != oldNode {
			c, e := s.loadDNSConfig(ctx)
			if e != nil {
				s.logger.Warn("bind change DNS config unavailable", "error", e)
			}
			for _, service := range services {
				if service.DnsName != "" {
					if p, e := s.dnsProvider(c); e == nil && p != nil {
						if e = s.setAppRecord(ctx, c, p, service.DnsName, nodeIP(node, c)); e != nil {
							s.logger.Warn("bind change DNS update failed", "dns", service.DnsName, "error", e)
						}
					}
				}
			}
			s.push(ctx, oldNode)
			for _, service := range services {
				s.nodes.ResetApp(service.ID)
				s.appEvent(service.ID, eventInfo, "Accès modifiés par approbation du superadmin")
			}
		}
	}
	if change.ResourceType == "app" {
		s.push(ctx, app.NodeID)
		if newNodeID != baseNode && app.DnsName != "" {
			c, e := s.loadDNSConfig(ctx)
			if e != nil {
				s.logger.Warn("bind change DNS update failed", "dns", app.DnsName, "error", e)
			} else if p, e := s.dnsProvider(c); e == nil && p != nil {
				if e = s.setAppRecord(ctx, c, p, app.DnsName, nodeIP(node, c)); e != nil {
					s.logger.Warn("bind change DNS update failed", "dns", app.DnsName, "error", e)
				}
			}
		}
	} else {
		if newNodeID != stack.NodeID {
			oldNode := stack.NodeID
			stack.NodeID = newNodeID
			for _, service := range services {
				s.nodes.ResetApp(service.ID)
			}
			s.push(ctx, oldNode)
		}
		s.push(ctx, stack.NodeID)
	}
	s.notifyBindRequest(row, bindRequestCreated)
	return nil
}
