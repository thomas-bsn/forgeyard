package api

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"net/http"
	"strings"
	"testing"
)

func putJSON(t *testing.T, c *http.Client, url string, body, out any) int {
	t.Helper()
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPut, url, strings.NewReader(string(b)))
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if out != nil {
		json.NewDecoder(resp.Body).Decode(out)
	}
	return resp.StatusCode
}

func TestProfile(t *testing.T) {
	ts, _ := newTestServerWithHandle(t)
	c := newClient()
	post(t, c, ts.URL+"/api/setup", setupRequest{Token: testToken, InstanceName: "F", Username: "boss",
		Password: "a-long-enough-password", PublicURL: "https://forgeyard.example.com"})

	var me userResponse
	if code := putJSON(t, c, ts.URL+"/api/me/profile", profileRequest{DisplayName: "Le Boss", Bio: "Admin du PaaS", Email: "boss@example.com"}, &me); code != http.StatusOK {
		t.Fatalf("profile: %d", code)
	}
	if me.DisplayName != "Le Boss" || me.Bio != "Admin du PaaS" || me.Email != "boss@example.com" || me.Method != "password" || me.NameFromDiscord {
		t.Fatalf("me: %+v", me)
	}
	if code := putJSON(t, c, ts.URL+"/api/me/profile", profileRequest{DisplayName: "x", Email: "not an email"}, nil); code != http.StatusBadRequest {
		t.Fatalf("bad email: %d", code)
	}

	// Pictures: raster images only, served back to signed-in users.
	var buf bytes.Buffer
	png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 4, 4)))
	pic := "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())
	if code := putJSON(t, c, ts.URL+"/api/me/avatar", map[string]string{"image": pic}, &me); code != http.StatusOK || !me.CustomAvatar {
		t.Fatalf("avatar upload: %d %+v", code, me)
	}
	resp, err := c.Get(ts.URL + me.AvatarURL)
	if err != nil {
		t.Fatal(err)
	}
	served, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.Header.Get("Content-Type") != "image/png" || !bytes.Equal(served, buf.Bytes()) {
		t.Fatalf("avatar served as %q", resp.Header.Get("Content-Type"))
	}
	if resp, _ := newClient().Get(ts.URL + me.AvatarURL); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("avatar for anonymous: %d", resp.StatusCode)
	}
	svg := "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString([]byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`))
	if code := putJSON(t, c, ts.URL+"/api/me/avatar", map[string]string{"image": svg}, nil); code != http.StatusBadRequest {
		t.Fatalf("svg accepted: %d", code)
	}

	// Changing the password needs the current one and signs out the other devices.
	other := newClient()
	post(t, other, ts.URL+"/api/auth/login", loginRequest{Username: "boss", Password: "a-long-enough-password"})
	var sessions []sessionResponse
	get(t, c, ts.URL+"/api/me/sessions", &sessions)
	if len(sessions) != 2 {
		t.Fatalf("sessions: %+v", sessions)
	}
	if code := putJSON(t, c, ts.URL+"/api/me/password", map[string]string{"current": "wrong-password-here", "new": "another-long-password"}, nil); code != http.StatusForbidden {
		t.Fatalf("wrong current password: %d", code)
	}
	if code := putJSON(t, c, ts.URL+"/api/me/password", map[string]string{"current": "a-long-enough-password", "new": "another-long-password"}, nil); code != http.StatusNoContent {
		t.Fatalf("password change: %d", code)
	}
	if code := get(t, other, ts.URL+"/api/auth/me", nil); code != http.StatusUnauthorized {
		t.Fatalf("other device still signed in: %d", code)
	}
	if code := get(t, c, ts.URL+"/api/auth/me", nil); code != http.StatusOK {
		t.Fatalf("this device signed out: %d", code)
	}
	if resp := post(t, newClient(), ts.URL+"/api/auth/login", loginRequest{Username: "boss", Password: "another-long-password"}); resp.StatusCode != http.StatusOK {
		t.Fatalf("login with the new password: %d", resp.StatusCode)
	}
	if code := del(t, c, ts.URL+"/api/me/sessions/others"); code != http.StatusNoContent {
		t.Fatalf("sign out the others: %d", code)
	}
	get(t, c, ts.URL+"/api/me/sessions", &sessions)
	if len(sessions) != 1 || !sessions[0].Current {
		t.Fatalf("sessions after signing out the others: %+v", sessions)
	}
}
