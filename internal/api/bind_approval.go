package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/thomas-bsn/forgeyard/internal/agentpb"
	"github.com/thomas-bsn/forgeyard/internal/dns"
	"github.com/thomas-bsn/forgeyard/internal/store/db"
)

func bindSpecs(mounts []bindMount) []*agentpb.BindMount {
	out := make([]*agentpb.BindMount, 0, len(mounts))
	for _, m := range mounts {
		out = append(out, &agentpb.BindMount{Source: m.Source, Target: m.Target, ReadOnly: m.ReadOnly == nil || *m.ReadOnly})
	}
	return out
}
func appBinds(app db.App) []*agentpb.BindMount {
	var mounts []*agentpb.BindMount
	_ = json.Unmarshal([]byte(app.BindMounts), &mounts)
	return mounts
}

func (s *Server) handleApproveBindRequest(w http.ResponseWriter, r *http.Request) {
	var empty struct{}
	if !decodeJSON(w, r, &empty) {
		return
	}
	row, ok := s.loadBindRequest(w, r)
	if !ok {
		return
	}
	if row.Status == "created" {
		s.writeBindRequest(w, r, row, http.StatusOK)
		return
	}
	if err := s.approveBindRequest(r, row); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	row, err := s.store.GetBindRequest(r.Context(), row.ID)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.writeBindRequest(w, r, row, http.StatusOK)
}

// approveBindRequest claims one immutable submission. The resource and its request link are committed
// together before DNS and any deployment, so retries can resume the same resource after partial failure.
func (s *Server) approveBindRequest(r *http.Request, row db.BindRequest) (result error) {
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	now := time.Now().Unix()
	claimed, err := s.store.ClaimBindRequest(ctx, db.ClaimBindRequestParams{DecidedAt: now, DecidedBy: sql.NullInt64{Int64: currentUser(r).ID, Valid: true}, ID: row.ID, DecidedAt_2: now - 120})
	if err != nil {
		return err
	}
	if claimed == 0 {
		return errors.New("cette demande n'est plus en attente ou est déjà en cours de traitement")
	}
	defer func() {
		if result != nil {
			cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = s.store.FailBindRequest(cleanup, db.FailBindRequestParams{Reason: result.Error(), ID: row.ID})
		}
	}()
	row, err = s.store.GetBindRequest(ctx, row.ID)
	if err != nil {
		return err
	}
	owner, err := s.store.GetUserByID(ctx, row.AuthorID)
	if err != nil {
		return err
	}
	if owner.Disabled != 0 || owner.AppsSuspended != 0 {
		return errors.New("le demandeur ne peut plus déployer d'apps")
	}
	var in bindRequestInput
	if err = json.Unmarshal([]byte(row.Config), &in); err != nil {
		return err
	}
	if msg := in.validate(); msg != "" {
		return errors.New(msg)
	}
	node, err := s.pickNode(ctx, row.NodeID)
	if err != nil {
		return errors.New("ce node n'est pas en ligne pour vérifier les montages")
	}
	if in.GPU != nil {
		if err := checkGpuNode(node, in.GPU); err != nil {
			return err
		}
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
	// An already linked resource keeps the original approved paths on a retry.
	if row.AppID.Valid && row.ApprovedMounts != canonical {
		return errors.New("les chemins ont changé depuis l'approbation : soumettez une nouvelle demande")
	}
	c, err := s.loadDNSConfig(ctx)
	if err != nil {
		return err
	}
	if s.reservedNames(ctx, r, c)[in.Name] {
		return errors.New("ce nom est réservé, choisissez-en un autre")
	}
	var app db.App
	if row.AppID.Valid {
		app, err = s.store.GetApp(ctx, row.AppID.Int64)
		if err != nil {
			return err
		}
	} else {
		if taken, err := s.store.NameTaken(ctx, in.Name); err != nil {
			return err
		} else if taken != 0 {
			return errors.New("une app ou un projet porte déjà ce nom")
		}
		if p, e := s.dnsProvider(c); e != nil && in.Kind == kindWeb {
			return e
		} else if p != nil && in.Kind == kindWeb {
			taken, e := dns.Exists(ctx, p, c.Zone, appHostname(c, in.Name))
			if e != nil {
				return fmt.Errorf("vérification du DNS : %w", e)
			}
			if taken {
				return errors.New("ce nom existe déjà dans le DNS")
			}
		}
		err = s.store.InTx(ctx, func(q *db.Queries) error {
			var e error
			if taken, e := q.NameTaken(ctx, in.Name); e != nil {
				return e
			} else if taken != 0 {
				return errors.New("une app ou un projet porte déjà ce nom")
			}
			app, e = q.CreateApp(ctx, db.CreateAppParams{Name: in.Name, OwnerID: owner.ID, NodeID: node.ID, Kind: in.Kind, Image: in.Image, Dockerfile: in.Dockerfile, Port: int64(in.Port), Volumes: string(must(json.Marshal(in.Volumes))), Gpu: sealGpu(in.GPU), EnvSealed: row.EnvSealed, MemoryMb: int64(in.MemoryMB), CreatedAt: now, UpdatedAt: now})
			if e != nil {
				return e
			}
			// Stage stopped until external DNS setup succeeds. No desired state is sent here.
			if _, e = q.SetAppRunning(ctx, db.SetAppRunningParams{ID: app.ID, Running: 0, UpdatedAt: now}); e != nil {
				return e
			}
			if e = q.SetAppBindMounts(ctx, db.SetAppBindMountsParams{ID: app.ID, BindMounts: canonical, BindAuthorized: 1}); e != nil {
				return e
			}
			if e = q.LinkBindRequestApp(ctx, db.LinkBindRequestAppParams{ID: row.ID, AppID: sql.NullInt64{Int64: app.ID, Valid: true}, ApprovedMounts: canonical}); e != nil {
				return e
			}
			if in.LogoImage != "" {
				data, contentType, e := decodeDataURL(in.LogoImage, maxAvatarBytes)
				if e != nil {
					return e
				}
				if e = q.PutAppLogoImage(ctx, db.PutAppLogoImageParams{AppID: app.ID, ContentType: contentType, Data: data}); e != nil {
					return e
				}
				if e = q.SetAppLogo(ctx, db.SetAppLogoParams{ID: app.ID, LogoMode: "custom", LogoUpdatedAt: time.Now().UnixMilli()}); e != nil {
					return e
				}
			}
			return nil
		})
		if err != nil {
			return fmt.Errorf("création de l'app : %w", err)
		}
	}
	if p, e := s.dnsProvider(c); e != nil && in.Kind == kindWeb {
		return e
	} else if p != nil && in.Kind == kindWeb {
		host := appHostname(c, app.Name)
		if e = s.setAppRecord(ctx, c, p, host, nodeIP(node, c)); e != nil {
			return fmt.Errorf("création du DNS : %w", e)
		}
		if e = s.store.SetAppDNSName(ctx, db.SetAppDNSNameParams{ID: app.ID, DnsName: host}); e != nil {
			return e
		}
	}
	err = s.store.InTx(ctx, func(q *db.Queries) error {
		current, e := q.GetApp(ctx, app.ID)
		if e != nil {
			return e
		}
		if current.BindAuthorized == 0 {
			return errBindRevoked
		}
		if _, e := q.SetAppRunning(ctx, db.SetAppRunningParams{ID: app.ID, Running: 1, UpdatedAt: time.Now().Unix()}); e != nil {
			return e
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
	s.appEvent(app.ID, eventInfo, "Bind mounts approuvés par "+currentUser(r).DisplayName)
	s.push(ctx, node.ID)
	return nil
}

func (s *Server) protectedBindPaths(node db.Node) []string {
	if node.IsLocal != 0 && s.dataDir != "" {
		if p, err := filepath.Abs(s.dataDir); err == nil {
			return []string{p}
		}
	}
	return nil
}

func (s *Server) handleValidateBindRequest(w http.ResponseWriter, r *http.Request) {
	var empty struct{}
	if !decodeJSON(w, r, &empty) {
		return
	}
	row, ok := s.loadBindRequest(w, r)
	if !ok {
		return
	}
	var in bindRequestInput
	if err := json.Unmarshal([]byte(row.Config), &in); err != nil {
		s.internalError(w, r, err)
		return
	}
	node, err := s.store.GetNode(r.Context(), row.NodeID)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	result, err := s.nodes.ValidateBindMounts(ctx, node.ID, bindSpecs(in.BindMounts), s.protectedBindPaths(node)...)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	if result.GetError() != "" {
		writeError(w, http.StatusConflict, result.GetError())
		return
	}
	writeJSON(w, http.StatusOK, bindAPI(result.GetMounts()))
}

func bindAPI(mounts []*agentpb.BindMount) []bindMount {
	out := make([]bindMount, 0, len(mounts))
	for _, m := range mounts {
		ro := m.GetReadOnly()
		out = append(out, bindMount{Source: m.GetSource(), Target: m.GetTarget(), ReadOnly: &ro})
	}
	return out
}
