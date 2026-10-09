package agent

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"time"
)

// joinFileContent is written by the server of the same Compose project to enable its machine as a node.
type joinFileContent struct {
	Server      string `json:"server"`
	AgentServer string `json:"agentServer"`
	Token       string `json:"token"`
	CA          string `json:"ca"`
}

// WaitAndJoin waits for the server to drop a join file at path, joins with it and deletes it. A file that
// fails (e.g. an expired token) is set aside and the wait goes on, for the admin to generate a new one.
func WaitAndJoin(ctx context.Context, path, stateDir string, logger *slog.Logger) (Config, error) {
	logger.Info("waiting for Forgeyard to enable this machine as a node", "join_file", path)
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for {
		raw, err := os.ReadFile(path)
		if err == nil {
			var j joinFileContent
			if err := json.Unmarshal(raw, &j); err != nil {
				logger.Error("unreadable join file", "err", err)
			} else {
				cfg, err := Join(ctx, JoinOptions{
					Server: j.Server, Token: j.Token, CAFingerprint: j.CA, StateDir: stateDir, AgentServer: j.AgentServer,
				})
				if err == nil {
					os.Remove(path)
					return cfg, nil
				}
				logger.Error("joining with the join file failed: generate a new one from the Nodes page", "err", err)
			}
			os.Rename(path, path+".failed")
		} else if !errors.Is(err, os.ErrNotExist) {
			logger.Error("reading the join file failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return Config{}, ctx.Err()
		case <-ticker.C:
		}
	}
}
