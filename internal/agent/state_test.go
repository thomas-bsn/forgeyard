package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestSaveAgentServer(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, configFile), []byte(`{"nodeId":3,"nodeName":"pi","agentServer":"forge.example.com:8081"}`), 0o644)
	if err := SaveAgentServer(dir, "192.168.1.39:8081"); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, configFile))
	var cfg Config
	if err := json.Unmarshal(raw, &cfg); err != nil || cfg.AgentServer != "192.168.1.39:8081" || cfg.NodeID != 3 || cfg.NodeName != "pi" {
		t.Fatalf("saved config: %s %v", raw, err)
	}
}
