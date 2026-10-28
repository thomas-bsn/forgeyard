package api

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/thomas-bsn/forgeyard/internal/auth"
	"github.com/thomas-bsn/forgeyard/internal/store/db"
)

// loginAs creates a local user and signs them in.
func loginAs(t *testing.T, ts, username string, server *Server) (*http.Client, db.User) {
	t.Helper()
	hash, _ := auth.HashPassword("a-long-enough-password")
	u, err := server.store.CreateLocalUser(context.Background(), db.CreateLocalUserParams{
		Username: nullString(username), PasswordHash: nullString(hash), DisplayName: username, Role: "user", CreatedAt: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	c := newClient()
	if resp := post(t, c, ts+"/api/auth/login", loginRequest{Username: username, Password: "a-long-enough-password"}); resp.StatusCode != http.StatusOK {
		t.Fatalf("login %s: %d", username, resp.StatusCode)
	}
	return c, u
}

func TestAppMembers(t *testing.T) {
	ts, server := newTestServerWithHandle(t)
	admin := newClient()
	post(t, admin, ts.URL+"/api/setup", setupRequest{Token: testToken, InstanceName: "F", Username: "boss",
		Password: "a-long-enough-password", PublicURL: "https://forgeyard.example.com"})
	fa := connectFakeAgent(t, ts.URL, server, admin, "node-a")
	fa.desired(t)

	lea, leaUser := loginAs(t, ts.URL, "lea", server)
	bob, bobUser := loginAs(t, ts.URL, "bob", server)
	eve, eveUser := loginAs(t, ts.URL, "eve", server)
	mallory, _ := loginAs(t, ts.URL, "mallory", server)

	var app appResponse
	if code := postJSON(t, lea, ts.URL+"/api/apps", appInput{Name: "shop", Image: "nginx", Port: 80, Env: map[string]string{"SECRET": "s3cret", "MODE": "prod"}}, &app); code != http.StatusCreated {
		t.Fatalf("create app: %d", code)
	}
	fa.desired(t)
	base := ts.URL + "/api/apps/" + itoa(app.ID)

	// Without a role the app does not exist.
	if code := get(t, mallory, base, nil); code != http.StatusNotFound {
		t.Fatalf("stranger reading: %d", code)
	}
	var list []appResponse
	get(t, bob, ts.URL+"/api/apps", &list)
	if len(list) != 0 {
		t.Fatalf("bob lists %d apps before being added", len(list))
	}

	// Only the owner manages members, and only existing, enabled users can be added.
	if code := put(t, bob, base+"/members/"+itoa(bobUser.ID), memberInput{Role: "contributor"}); code != http.StatusNotFound {
		t.Fatalf("stranger adding themselves: %d", code)
	}
	if code := put(t, lea, base+"/members/"+itoa(bobUser.ID), memberInput{Role: "admin"}); code != http.StatusBadRequest {
		t.Fatalf("unknown role: %d", code)
	}
	if code := put(t, lea, base+"/members/"+itoa(leaUser.ID), memberInput{Role: "viewer"}); code != http.StatusConflict {
		t.Fatalf("owner as member: %d", code)
	}
	if code := put(t, lea, base+"/members/9999", memberInput{Role: "viewer"}); code != http.StatusNotFound {
		t.Fatalf("missing user: %d", code)
	}
	if code := put(t, lea, base+"/members/"+itoa(bobUser.ID), memberInput{Role: "contributor"}); code != http.StatusOK {
		t.Fatalf("adding bob: %d", code)
	}
	if code := put(t, lea, base+"/members/"+itoa(eveUser.ID), memberInput{Role: "viewer"}); code != http.StatusOK {
		t.Fatalf("adding eve: %d", code)
	}

	// Everyone with access sees who has it.
	var members membersResponse
	if code := get(t, eve, base+"/members", &members); code != http.StatusOK || members.Owner.DisplayName != "lea" || len(members.Members) != 2 || members.Role != "viewer" {
		t.Fatalf("members as viewer: %d %+v", code, members)
	}

	// The list shows shared apps with the role.
	get(t, bob, ts.URL+"/api/apps", &list)
	if len(list) != 1 || list[0].Role != "contributor" || list[0].Via != "" {
		t.Fatalf("bob's list: %+v", list)
	}

	// A contributor deploys and edits, but does not read secrets, delete, share or open a shell.
	var got appResponse
	if code := get(t, bob, base, &got); code != http.StatusOK || !slices.Equal(got.EnvKeys, []string{"MODE", "SECRET"}) {
		t.Fatalf("contributor reading: %d %+v", code, got)
	}
	if code := get(t, bob, base+"/env", nil); code != http.StatusForbidden {
		t.Fatalf("contributor revealing the environment: %d", code)
	}
	if code := postJSON(t, bob, base+"/redeploy", struct{}{}, nil); code != http.StatusOK {
		t.Fatalf("contributor redeploying: %d", code)
	}
	if code := del(t, bob, base); code != http.StatusForbidden {
		t.Fatalf("contributor deleting: %d", code)
	}
	if code := put(t, bob, base+"/members/"+itoa(eveUser.ID), memberInput{Role: "contributor"}); code != http.StatusForbidden {
		t.Fatalf("contributor promoting a viewer: %d", code)
	}
	if code := get(t, bob, base+"/terminal", nil); code != http.StatusForbidden {
		t.Fatalf("contributor opening a shell: %d", code)
	}
	if code := put(t, bob, base+"/public", map[string]bool{"public": true}); code != http.StatusForbidden {
		t.Fatalf("contributor publishing: %d", code)
	}

	// Editing: a kept variable keeps its value without the form ever seeing it.
	edit := appInput{Image: "nginx:1.27", Port: 80, Env: map[string]string{"MODE": "staging"}, KeepEnv: []string{"SECRET"}, MemoryMB: 512}
	if code := put(t, bob, base, edit); code != http.StatusOK {
		t.Fatalf("contributor editing: %d", code)
	}
	var revealed envReveal
	if code := get(t, lea, base+"/env", &revealed); code != http.StatusOK || revealed.Env["SECRET"] != "s3cret" || revealed.Env["MODE"] != "staging" {
		t.Fatalf("owner revealing: %d %+v", code, revealed)
	}
	edit.KeepEnv = []string{"MISSING"}
	if code := put(t, bob, base, edit); code != http.StatusBadRequest {
		t.Fatalf("keeping a variable that does not exist: %d", code)
	}
	// A variable left out of both lists is dropped.
	edit.KeepEnv = nil
	if code := put(t, bob, base, edit); code != http.StatusOK {
		t.Fatalf("dropping a variable: %d", code)
	}
	revealed = envReveal{}
	get(t, lea, base+"/env", &revealed)
	if _, still := revealed.Env["SECRET"]; still || len(revealed.Env) != 1 {
		t.Fatalf("variables after drop: %+v", revealed.Env)
	}
	// A contributor cannot hand themselves a GPU.
	edit.GPU = &gpuRequest{All: true}
	if code := put(t, bob, base, edit); code != http.StatusForbidden {
		t.Fatalf("contributor requesting a GPU: %d", code)
	}

	// A viewer reads the state and nothing else.
	got = appResponse{}
	if code := get(t, eve, base, &got); code != http.StatusOK || got.EnvKeys != nil || got.Dockerfile != "" {
		t.Fatalf("viewer reading: %d %+v", code, got)
	}
	if code := postJSON(t, eve, base+"/stop", struct{}{}, nil); code != http.StatusForbidden {
		t.Fatalf("viewer stopping: %d", code)
	}
	if code := put(t, eve, base, edit); code != http.StatusForbidden {
		t.Fatalf("viewer editing: %d", code)
	}

	// Events say who did it.
	var events []appEventResponse
	get(t, lea, base+"/events", &events)
	if !slices.ContainsFunc(events, func(e appEventResponse) bool { return e.Message == "lea donne le rôle contributor à bob" }) {
		t.Fatalf("no event for the new member: %+v", events)
	}

	// Taking a role away hides the app again.
	if code := del(t, lea, base+"/members/"+itoa(bobUser.ID)); code != http.StatusOK {
		t.Fatalf("removing bob: %d", code)
	}
	if code := get(t, bob, base, nil); code != http.StatusNotFound {
		t.Fatalf("removed member reading: %d", code)
	}
	if code := del(t, lea, base+"/members/"+itoa(bobUser.ID)); code != http.StatusNotFound {
		t.Fatalf("removing twice: %d", code)
	}

	// A suspended owner suspends the app for its contributors too.
	if _, err := server.store.DB.ExecContext(context.Background(), `UPDATE users SET apps_suspended = 1 WHERE id = ?`, leaUser.ID); err != nil {
		t.Fatal(err)
	}
	if code := postJSON(t, eve, base+"/start", struct{}{}, nil); code != http.StatusForbidden {
		t.Fatalf("viewer starting: %d", code)
	}
	put(t, lea, base+"/members/"+itoa(bobUser.ID), memberInput{Role: "contributor"})
	if code := postJSON(t, bob, base+"/start", struct{}{}, nil); code != http.StatusForbidden {
		t.Fatalf("contributor starting a suspended app: %d", code)
	}
	if _, err := server.store.DB.ExecContext(context.Background(), `UPDATE users SET apps_suspended = 0 WHERE id = ?`, leaUser.ID); err != nil {
		t.Fatal(err)
	}

	// Handing the app over: the new owner holds everything, the former one nothing.
	if code := postJSON(t, bob, base+"/transfer", transferInput{UserID: bobUser.ID}, nil); code != http.StatusForbidden {
		t.Fatalf("contributor transferring: %d", code)
	}
	if code := postJSON(t, lea, base+"/transfer", transferInput{UserID: bobUser.ID}, nil); code != http.StatusOK {
		t.Fatalf("transfer: %d", code)
	}
	if code := get(t, lea, base, nil); code != http.StatusNotFound {
		t.Fatalf("former owner reading: %d", code)
	}
	if code := get(t, bob, base+"/env", nil); code != http.StatusOK {
		t.Fatalf("new owner revealing: %d", code)
	}
	if rows, _ := server.store.ListMembershipsOfUser(context.Background(), bobUser.ID); len(rows) != 0 {
		t.Fatalf("the new owner kept a member row: %+v", rows)
	}

	// Deleting the app deletes its members.
	if code := del(t, bob, base); code != http.StatusNoContent {
		t.Fatalf("deleting: %d", code)
	}
	if rows, _ := server.store.ListMembershipsOfUser(context.Background(), eveUser.ID); len(rows) != 0 {
		t.Fatalf("members outlived their app: %+v", rows)
	}
}

func TestProjectMembers(t *testing.T) {
	url, server, _, fa := setupStackServer(t)
	lea, _ := loginAs(t, url, "lea", server)
	bob, bobUser := loginAs(t, url, "bob", server)
	eve, eveUser := loginAs(t, url, "eve", server)

	in := stackInput{Name: "shameless", Compose: shamelessCompose, Env: map[string]string{"DB_PASSWORD": "pw"}}
	var st stackResponse
	if code := postJSON(t, lea, url+"/api/stacks", in, &st); code != http.StatusCreated {
		t.Fatalf("create: %d", code)
	}
	fa.desired(t)
	base := url + "/api/stacks/" + itoa(st.ID)
	if code := get(t, bob, base, nil); code != http.StatusOK {
		// Another member's project is readable, with its public services only.
		t.Fatalf("bob reading before being added: %d", code)
	}
	var before stackResponse
	get(t, bob, base, &before)
	if before.Compose != "" || before.EnvKeys != nil || len(before.Volumes) != 0 {
		t.Fatalf("a stranger sees what only members see: %+v", before)
	}

	if code := put(t, lea, base+"/members/"+itoa(bobUser.ID), memberInput{Role: "contributor"}); code != http.StatusOK {
		t.Fatalf("adding bob: %d", code)
	}
	if code := put(t, lea, base+"/members/"+itoa(eveUser.ID), memberInput{Role: "viewer"}); code != http.StatusOK {
		t.Fatalf("adding eve: %d", code)
	}

	// The services of the project come with it.
	var apps []appResponse
	get(t, bob, url+"/api/apps", &apps)
	if len(apps) != 4 {
		t.Fatalf("bob lists %d apps, want the 4 services", len(apps))
	}
	for _, a := range apps {
		if a.Role != "contributor" || a.Via != "project" {
			t.Fatalf("service %s: role %q via %q", a.Name, a.Role, a.Via)
		}
	}
	var stacks []stackResponse
	get(t, bob, url+"/api/stacks", &stacks)
	if len(stacks) != 1 || stacks[0].Role != "contributor" {
		t.Fatalf("bob's projects: %+v", stacks)
	}

	// A contributor sees the compose file and the variable names; a viewer sees the state only.
	var asBob, asEve stackResponse
	get(t, bob, base, &asBob)
	get(t, eve, base, &asEve)
	if asBob.Compose == "" || !slices.Equal(asBob.EnvKeys, []string{"DB_PASSWORD"}) {
		t.Fatalf("contributor view: %+v", asBob)
	}
	if asEve.Compose != "" || asEve.EnvKeys != nil || len(asEve.Services) != 4 {
		t.Fatalf("viewer view: compose %q, env %v, %d services", asEve.Compose, asEve.EnvKeys, len(asEve.Services))
	}

	// Contributor edits keep the secret, and cannot add a host folder.
	in.Env, in.KeepEnv = nil, []string{"DB_PASSWORD"}
	if code := put(t, bob, base, in); code != http.StatusOK {
		t.Fatalf("contributor editing: %d", code)
	}
	var revealed envReveal
	if get(t, lea, base+"/env", &revealed); revealed.Env["DB_PASSWORD"] != "pw" {
		t.Fatalf("kept variable: %+v", revealed)
	}
	withBind := in
	withBind.Compose = strings.Replace(shamelessCompose, "  web:\n    image: ghcr.io/shameless/web:latest\n", "  web:\n    image: ghcr.io/shameless/web:latest\n    volumes: [\"/:/host\"]\n", 1)
	if withBind.Compose == shamelessCompose {
		t.Fatal("the test compose did not change")
	}
	if code := put(t, bob, base, withBind); code != http.StatusBadRequest {
		t.Fatalf("contributor adding a host folder: %d", code)
	}
	if code := get(t, bob, base+"/env", nil); code != http.StatusForbidden {
		t.Fatalf("contributor revealing: %d", code)
	}
	if code := del(t, bob, base); code != http.StatusForbidden {
		t.Fatalf("contributor deleting: %d", code)
	}
	if code := put(t, eve, base, in); code != http.StatusForbidden {
		t.Fatalf("viewer editing: %d", code)
	}

	// Handing the project over hands its services over.
	if code := postJSON(t, lea, base+"/transfer", transferInput{UserID: bobUser.ID}, nil); code != http.StatusOK {
		t.Fatalf("transfer: %d", code)
	}
	apps = nil
	get(t, bob, url+"/api/apps", &apps)
	for _, a := range apps {
		if a.OwnerID != bobUser.ID || a.Role != "" {
			t.Fatalf("service %s after transfer: owner %d role %q", a.Name, a.OwnerID, a.Role)
		}
	}
	apps = nil
	if get(t, lea, url+"/api/apps", &apps); len(apps) != 0 {
		t.Fatalf("former owner still lists %d apps", len(apps))
	}

	if code := del(t, bob, base); code != http.StatusNoContent {
		t.Fatalf("deleting: %d", code)
	}
	if rows, _ := server.store.ListMembershipsOfUser(context.Background(), eveUser.ID); len(rows) != 0 {
		t.Fatalf("members outlived their project: %+v", rows)
	}
}
