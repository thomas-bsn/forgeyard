package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/thomas-bsn/forgeyard/internal/agentpb"
	"github.com/thomas-bsn/forgeyard/internal/compose"
	"github.com/thomas-bsn/forgeyard/internal/store/db"
)

// approveStackBindRequest verifies every source on the selected agent, stages one stopped stack and all
// its services in a single transaction, then enables them only after DNS setup succeeds. The linked
// stack ID makes retries resume the same resources.
func (s *Server) approveStackBindRequest(ctx context.Context, r *http.Request, row db.BindRequest, owner db.User, in *bindRequestInput) error {
	secrets, err := s.openEnv(row.EnvSealed)
	if err != nil {
		return err
	}
	reg := registryInput{}
	if in.Stack.Registry != nil {
		reg = *in.Stack.Registry
	}
	if raw := secrets["__FORGEYARD_STACK_REGISTRY"]; raw != "" {
		delete(secrets, "__FORGEYARD_STACK_REGISTRY")
		if err := json.Unmarshal([]byte(raw), &reg); err != nil {
			return err
		}
	}
	in.Stack.Env = secrets
	if in.Stack.Registry == nil && reg.Server != "" {
		in.Stack.Registry = &reg
	}
	parsed, msg, errs := in.Stack.parse(true)
	if msg != "" {
		return errors.New(msg)
	}
	if errs != nil {
		return errs
	}
	if in.validate() != "" {
		return errors.New(in.validate())
	}
	node, err := s.pickNode(ctx, row.NodeID)
	if err != nil {
		return errors.New("ce node n'est pas en ligne pour vérifier les montages")
	}
	check, err := s.nodes.ValidateBindMounts(ctx, node.ID, bindSpecs(in.BindMounts), s.protectedBindPaths(node)...)
	if err != nil {
		return err
	}
	if check.GetError() != "" {
		return errors.New(check.GetError())
	}
	if len(check.GetMounts()) != len(in.BindMounts) {
		return errors.New("réponse de vérification des montages incomplète")
	}
	for i, m := range check.GetMounts() {
		if !strings.HasPrefix(m.GetSource(), "/") || m.GetTarget() != in.BindMounts[i].Target || m.GetReadOnly() != *in.BindMounts[i].ReadOnly {
			return errors.New("réponse de vérification des montages invalide")
		}
	}
	canonical := string(must(json.Marshal(check.GetMounts())))
	if row.StackID.Valid && row.ApprovedMounts != canonical {
		return errors.New("les chemins ont changé depuis l'approbation : soumettez une nouvelle demande")
	}
	// Store the agent's canonical source paths in service specs and app bind authorization.
	byService := map[string][]*agentpb.BindMount{}
	for i, m := range check.GetMounts() {
		byService[in.BindMounts[i].Service] = append(byService[in.BindMounts[i].Service], m)
	}
	for i := range parsed.Services {
		svc := &parsed.Services[i]
		var binds []compose.Mount
		for _, m := range byService[svc.Name] {
			binds = append(binds, compose.Mount{Kind: "bind", Source: m.Source, Target: m.Target, ReadOnly: m.ReadOnly})
		}
		for _, m := range svc.Volumes {
			if m.Kind != "bind" {
				binds = append(binds, m)
			}
		}
		svc.Volumes = binds
	}
	want := planServices(in.Stack.Name, parsed)
	c, err := s.loadDNSConfig(ctx)
	if err != nil {
		return err
	}
	if !row.StackID.Valid {
		allNames := append([]plannedApp{{Name: in.Stack.Name, Kind: kindService}}, want...)
		if status, reason := s.checkStackNames(ctx, r, c, allNames, nil); status != 0 {
			return errors.New(reason)
		}
	}
	sealedEnv, err := s.sealStackEnv(secrets)
	if err != nil {
		return err
	}
	sealedRegistry, registryMessage, err := s.resolveRegistry(&reg, "")
	if err != nil {
		return err
	}
	if registryMessage != "" {
		return errors.New(registryMessage)
	}
	var st db.Stack
	var apps []db.App
	if row.StackID.Valid {
		st, err = s.store.GetStack(ctx, row.StackID.Int64)
		if err != nil {
			return err
		}
		apps, err = s.store.ListStackApps(ctx, st.ID)
		if err != nil {
			return err
		}
	} else {
		now := time.Now().Unix()
		err = s.store.InTx(ctx, func(q *db.Queries) error {
			var e error
			if taken, e := q.NameTaken(ctx, in.Stack.Name); e != nil {
				return e
			} else if taken != 0 {
				return errors.New("un nom du projet est déjà pris")
			}
			st, e = q.CreateStack(ctx, db.CreateStackParams{Name: in.Stack.Name, OwnerID: owner.ID, NodeID: node.ID, Compose: in.Stack.Compose, EnvSealed: sealedEnv, RegistrySealed: sealedRegistry, CreatedAt: now, UpdatedAt: now})
			if e != nil {
				return e
			}
			for _, p := range want {
				a, e := s.createStackApp(ctx, q, st, p)
				if e != nil {
					return e
				}
				mounts := []*agentpb.BindMount{}
				for _, m := range byService[p.Service] {
					mounts = append(mounts, m)
				}
				if len(mounts) > 0 {
					if e = q.SetAppBindMounts(ctx, db.SetAppBindMountsParams{ID: a.ID, BindMounts: string(must(json.Marshal(mounts))), BindAuthorized: 1}); e != nil {
						return e
					}
				}
				if _, e = q.SetAppRunning(ctx, db.SetAppRunningParams{ID: a.ID, Running: 0, UpdatedAt: now}); e != nil {
					return e
				}
				apps = append(apps, a)
			}
			return q.LinkBindRequestStack(ctx, db.LinkBindRequestStackParams{StackID: sql.NullInt64{Int64: st.ID, Valid: true}, ApprovedMounts: canonical, ID: row.ID})
		})
		if err != nil {
			return fmt.Errorf("création du projet : %w", err)
		}
	}
	for _, a := range apps {
		if a.Kind == kindWeb {
			if err := s.createAppRecord(ctx, c, a, node); err != nil {
				return err
			}
		}
	}
	err = s.store.InTx(ctx, func(q *db.Queries) error {
		for _, a := range apps {
			if _, e := q.SetAppRunning(ctx, db.SetAppRunningParams{ID: a.ID, Running: 1, UpdatedAt: time.Now().Unix()}); e != nil {
				return e
			}
		}
		completed, e := q.CompleteBindRequest(ctx, row.ID)
		if e != nil {
			return e
		}
		if completed != 1 {
			return errors.New("la demande a changé pendant la création")
		}
		return nil
	})
	if err != nil {
		return err
	}
	for _, a := range apps {
		s.appEvent(a.ID, eventInfo, "Projet Compose créé après approbation des bind mounts par "+currentUser(r).DisplayName)
	}
	s.push(ctx, node.ID)
	return nil
}
