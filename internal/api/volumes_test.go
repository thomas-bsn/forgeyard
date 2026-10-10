package api

import (
	"net/http"
	"slices"
	"testing"
)

func TestAppVolumes(t *testing.T) {
	ts, server := newTestServerWithHandle(t)
	admin := newClient()
	post(t, admin, ts.URL+"/api/setup", setupRequest{Token: testToken, InstanceName: "F", Username: "boss",
		Password: "a-long-enough-password", PublicURL: "https://forgeyard.example.com"})
	fa := connectFakeAgent(t, ts.URL, server, admin, "nas")
	fa.desired(t)

	for _, bad := range [][]string{{"data"}, {"/"}, {"/proc/x"}, {"/data", "/data/"}, {"/a:b"}} {
		if code := postJSON(t, admin, ts.URL+"/api/apps", appInput{Name: "db", Image: "postgres", Port: 5432, Volumes: bad}, nil); code != http.StatusBadRequest {
			t.Fatalf("%v accepted: %d", bad, code)
		}
	}
	var app appResponse
	if code := postJSON(t, admin, ts.URL+"/api/apps", appInput{Name: "db", Image: "postgres", Port: 5432, Volumes: []string{"/var/lib/postgresql/data/"}}, &app); code != http.StatusCreated {
		t.Fatalf("create: %d", code)
	}
	if len(app.Volumes) != 1 || app.Volumes[0].Path != "/var/lib/postgresql/data" || app.Volumes[0].SizeBytes != -1 {
		t.Fatalf("volumes: %+v", app.Volumes)
	}
	if spec := fa.desired(t).GetApps()[0]; !slices.Equal(spec.GetVolumes(), []string{"/var/lib/postgresql/data"}) {
		t.Fatalf("spec volumes: %v", spec.GetVolumes())
	}

	// Its data does not follow a move yet: the move must say it knows.
	fb := connectFakeAgent(t, ts.URL, server, admin, "pi")
	fb.desired(t)
	if code := postJSON(t, admin, ts.URL+"/api/admin/apps/"+itoa(app.ID)+"/move", map[string]any{"nodeId": fb.nodeID}, nil); code != http.StatusConflict {
		t.Fatalf("move without confirmation: %d", code)
	}

	// Deleting the app deletes its data on every node it may have left some.
	if code := del(t, admin, ts.URL+"/api/apps/"+itoa(app.ID)); code != http.StatusNoContent {
		t.Fatalf("delete: %d", code)
	}
	for _, f := range []*fakeAgent{fa, fb} {
		for {
			msg, err := f.stream.Recv()
			if err != nil {
				t.Fatal(err)
			}
			if d := msg.GetDropAppData(); d != nil {
				if d.GetAppId() != app.ID {
					t.Fatalf("dropped %d", d.GetAppId())
				}
				break
			}
		}
	}
}
