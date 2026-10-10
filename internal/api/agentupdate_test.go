package api

import (
	"net/http"
	"testing"
	"time"

	"github.com/thomas-bsn/forgeyard/internal/agentpb"
	"github.com/thomas-bsn/forgeyard/internal/version"
)

func TestAgentUpdates(t *testing.T) {
	ts, server := newTestServerWithHandle(t)
	admin := newClient()
	post(t, admin, ts.URL+"/api/setup", setupRequest{Token: testToken, InstanceName: "F", Username: "boss",
		Password: "a-long-enough-password", PublicURL: "https://forgeyard.example.com"})
	server.agentImage = "ghcr.io/me/forgeyard-agent:latest"
	before := version.Commit
	version.Commit = "0123456789abcdef0123456789abcdef01234567"
	t.Cleanup(func() { version.Commit = before })

	// An outdated agent that can replace itself is asked to, with the image of the server's commit.
	fa := connectFakeAgent(t, ts.URL, server, admin, "pi", func(h *agentpb.Hello) { h.AgentVersion, h.SelfUpdate = "old", true })
	want := "ghcr.io/me/forgeyard-agent:sha-" + version.Commit
	waitUpdate := func() {
		t.Helper()
		for {
			msg, err := fa.stream.Recv()
			if err != nil {
				t.Fatal(err)
			}
			if u := msg.GetUpdateAgent(); u != nil {
				if u.GetImage() != want {
					t.Fatalf("update image: %q", u.GetImage())
				}
				return
			}
		}
	}
	waitUpdate()
	var list []nodeResponse
	get(t, admin, ts.URL+"/api/admin/nodes", &list)
	if len(list) != 1 || !list[0].AgentOutdated || !list[0].Updating {
		t.Fatalf("node while updating: %+v", list)
	}

	// Its failure shows on the Nodes page; an admin retries by hand.
	fa.stream.Send(&agentpb.AgentMessage{Msg: &agentpb.AgentMessage_UpdateFailed{UpdateFailed: &agentpb.UpdateFailed{Error: "image introuvable"}}})
	deadline := time.Now().Add(5 * time.Second)
	for {
		list = nil
		get(t, admin, ts.URL+"/api/admin/nodes", &list)
		if list[0].UpdateError == "image introuvable" && !list[0].Updating {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("failure not shown: %+v", list[0])
		}
		time.Sleep(20 * time.Millisecond)
	}
	if code := postJSON(t, admin, ts.URL+"/api/admin/nodes/"+itoa(fa.nodeID)+"/update-agent", struct{}{}, nil); code != http.StatusAccepted {
		t.Fatalf("manual update: %d", code)
	}
	waitUpdate()

	// Turned off, an outdated agent connecting is left alone; one that cannot replace itself refuses.
	var settings agentSettings
	if code := putJSON(t, admin, ts.URL+"/api/admin/settings/agents", agentSettings{AutoUpdate: false}, &settings); code != http.StatusOK || settings.AutoUpdate || settings.ServerVersion != version.Commit {
		t.Fatalf("settings: %d %+v", code, settings)
	}
	fb := connectFakeAgent(t, ts.URL, server, admin, "nas", func(h *agentpb.Hello) { h.AgentVersion = "old" })
	if code := postJSON(t, admin, ts.URL+"/api/admin/nodes/"+itoa(fb.nodeID)+"/update-agent", struct{}{}, nil); code != http.StatusConflict {
		t.Fatalf("update of an agent that cannot replace itself: %d", code)
	}
}
