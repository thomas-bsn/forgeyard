package api

import (
	"maps"
	"net/http"
	"slices"
)

// keepEnv builds the variables an edit saves. in holds the values the form sent, keep the names of the
// variables it left untouched: they keep the value stored in old, which the form never saw. A name in
// neither is dropped.
func keepEnv(in map[string]string, keep []string, old map[string]string) (map[string]string, string) {
	out := make(map[string]string, len(in)+len(keep))
	maps.Copy(out, in)
	for _, name := range keep {
		if _, given := in[name]; given {
			continue
		}
		v, ok := old[name]
		if !ok {
			return nil, "la variable " + name + " n'existe pas encore : donnez-lui une valeur"
		}
		out[name] = v
	}
	return out, ""
}

func envKeys(env map[string]string) []string {
	if len(env) == 0 {
		return nil
	}
	return slices.Sorted(maps.Keys(env))
}

type envReveal struct {
	Env map[string]string `json:"env"`
}

// handleRevealAppEnv shows an app's variables in clear, to its owner only, and leaves a trace.
func (s *Server) handleRevealAppEnv(w http.ResponseWriter, r *http.Request) {
	a, ok := s.appFromPath(w, r, actEnvReveal)
	if !ok {
		return
	}
	env, err := s.openEnv(a.EnvSealed)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if env == nil {
		env = map[string]string{}
	}
	s.appEventBy(a.ID, currentUser(r).ID, eventWarning, "Variables d'environnement affichées par "+currentUser(r).DisplayName)
	writeJSON(w, http.StatusOK, envReveal{Env: env})
}

// handleRevealStackEnv does the same for a project's variables.
func (s *Server) handleRevealStackEnv(w http.ResponseWriter, r *http.Request) {
	st, ok := s.stackFromPath(w, r, actEnvReveal)
	if !ok {
		return
	}
	env, err := s.openStackEnv(st.EnvSealed)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if env == nil {
		env = map[string]string{}
	}
	s.eventsFor(r, stackResource(st), "Variables d'environnement affichées par "+currentUser(r).DisplayName)
	writeJSON(w, http.StatusOK, envReveal{Env: env})
}
