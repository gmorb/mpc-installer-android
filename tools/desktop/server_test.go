package main

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

const tok = "testtoken"

type harness struct {
	t   *testing.T
	srv *httptest.Server
	app *App
	fd  *fakeDevice
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	fd := newFakeDevice(t)
	srv := httptest.NewUnstartedServer(nil)
	srv.Start()
	t.Cleanup(srv.Close)
	work := t.TempDir()
	app := NewApp(fd.cfg(), tok, srv.Listener.Addr().String(), "http://127.0.0.1:1/none.json", work)
	srv.Config.Handler = app.Handler()
	return &harness{t, srv, app, fd}
}

func (h *harness) do(method, path string, body io.Reader, hdr map[string]string) (*http.Response, []byte) {
	h.t.Helper()
	req, _ := http.NewRequest(method, h.srv.URL+path, body)
	req.Header.Set("X-MPC-Token", tok)
	for k, v := range hdr {
		if v == "" {
			req.Header.Del(k)
		} else {
			req.Header.Set(k, v)
		}
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, b
}

func (h *harness) post(path string, v any) (int, map[string]any) {
	b, _ := json.Marshal(v)
	resp, out := h.do("POST", path, bytes.NewReader(b), map[string]string{"Content-Type": "application/json"})
	var m map[string]any
	json.Unmarshal(out, &m)
	return resp.StatusCode, m
}

func TestGuardRefusesEverythingWithoutTheToken(t *testing.T) {
	h := newHarness(t)
	if r, _ := h.do("GET", "/api/state", nil, map[string]string{"X-MPC-Token": ""}); r.StatusCode != 403 {
		t.Errorf("no token: %d", r.StatusCode)
	}
	if r, _ := h.do("GET", "/api/state", nil, map[string]string{"X-MPC-Token": "nope"}); r.StatusCode != 403 {
		t.Errorf("wrong token: %d", r.StatusCode)
	}
	if r, _ := h.do("GET", "/", nil, nil); r.StatusCode != 403 {
		t.Errorf("the page needs ?t=: %d", r.StatusCode)
	}
	if r, b := h.do("GET", "/?t="+tok, nil, nil); r.StatusCode != 200 || !strings.Contains(string(b), "<h1>MPC Installer</h1>") {
		t.Errorf("the page with the token: %d", r.StatusCode)
	} else if csp := r.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'none'") || !strings.Contains(csp, "frame-ancestors 'none'") || !strings.Contains(csp, "font-src data:") {
		t.Errorf("CSP: %q", csp)
	}
	if r, _ := h.do("GET", "/api/state", nil, nil); r.StatusCode != 200 {
		t.Errorf("token accepted: %d", r.StatusCode)
	}
}

func TestGuardRefusesAForeignHostOrOrigin(t *testing.T) {
	h := newHarness(t)
	req, _ := http.NewRequest("GET", h.srv.URL+"/api/state", nil)
	req.Host = "evil.example:80" // a DNS-rebound page
	req.Header.Set("X-MPC-Token", tok)
	if resp, _ := http.DefaultClient.Do(req); resp.StatusCode != 403 {
		t.Errorf("foreign host: %d", resp.StatusCode)
	}
	if r, _ := h.do("POST", "/api/disconnect", strings.NewReader("{}"), map[string]string{"Origin": "https://evil.example"}); r.StatusCode != 403 {
		t.Errorf("foreign origin: %d", r.StatusCode)
	}
	if r, _ := h.do("POST", "/api/disconnect", strings.NewReader("{}"), map[string]string{"Origin": "http://" + h.srv.Listener.Addr().String()}); r.StatusCode != 200 {
		t.Errorf("own origin: %d", r.StatusCode)
	}
}

func TestInstallNeedsConfirmationAndAConnection(t *testing.T) {
	h := newHarness(t)
	if code, _ := h.post("/api/install", map[string]any{"catalog": []string{"x"}, "confirm": true}); code != 400 {
		t.Errorf("not connected: %d", code)
	}
	if code, m := h.post("/api/connect", map[string]string{"host": "127.0.0.1", "password": "secret"}); code != 200 || m["device"] == nil {
		t.Fatalf("connect: %d %v", code, m)
	}
	if code, _ := h.post("/api/install", map[string]any{"uploads": []string{"nothing"}}); code != 400 {
		t.Errorf("without confirm: %d", code)
	}
	if code, m := h.post("/api/connect", map[string]string{"host": "127.0.0.1", "password": "bad"}); code != 400 || m["error"] == nil {
		t.Errorf("wrong password: %d %v", code, m)
	}
}

func upload(t *testing.T, h *harness, files map[string][]byte) map[string]any {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for name, data := range files {
		fw, _ := mw.CreateFormFile("file", name)
		fw.Write(data)
	}
	mw.Close()
	resp, out := h.do("POST", "/api/upload", &buf, map[string]string{"Content-Type": mw.FormDataContentType()})
	if resp.StatusCode != 200 {
		t.Fatalf("upload: %d %s", resp.StatusCode, out)
	}
	var m map[string]any
	json.Unmarshal(out, &m)
	return m
}

func TestWholeFlowThroughTheAPI(t *testing.T) {
	h := newHarness(t)
	if code, _ := h.post("/api/connect", map[string]string{"host": "127.0.0.1", "password": "secret"}); code != 200 {
		t.Fatal("connect")
	}
	good := installerPkg(t, "A-1", "a-plug", "me - VST - A", fakeInstaller("A", true, 0))
	zipBytes, _ := os.ReadFile(good.Path)
	res := upload(t, h, map[string][]byte{"A-1.zip": zipBytes, "notes.zip": []byte("not a zip")})["results"].([]any)
	var handle string
	bad := 0
	for _, r := range res {
		m := r.(map[string]any)
		if m["package"] != nil {
			handle = m["package"].(map[string]any)["handle"].(string)
		} else if m["error"] != nil {
			bad++
		}
	}
	if handle == "" || bad != 1 {
		t.Fatalf("one valid zip and one refused: %v", res)
	}
	code, plan := h.post("/api/plan", map[string]any{"uploads": []string{handle}})
	if code != 200 || plan["restarts"].(float64) != 1 || plan["maybeMore"].(bool) {
		t.Fatalf("plan: %d %v", code, plan)
	}
	if code, _ := h.post("/api/install", map[string]any{"uploads": []string{handle}, "confirm": true}); code != 400 {
		t.Fatal("an install that names no location must be refused, not sent to the internal drive")
	}
	if code, m := h.post("/api/install", map[string]any{"uploads": []string{handle}, "root": h.fd.cfg().SynthsDir, "confirm": true}); code != 200 {
		t.Fatalf("install: %d %v", code, m)
	}
	var last map[string]any
	for i := 0; i < 100; i++ {
		resp, b := h.do("GET", "/api/job?since=0", nil, nil)
		if resp.StatusCode != 200 {
			t.Fatal(resp.StatusCode)
		}
		json.Unmarshal(b, &last)
		if last["state"] != "running" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if last["state"] != "done" {
		t.Fatalf("job: %v", last)
	}
	if got := strings.Join(h.fd.calls(), ","); got != "stop acvs,start acvs" {
		t.Errorf("MPC calls: %s", got)
	}
	if _, err := os.Stat(h.fd.cfg().SynthsDir + "/me - VST - A/bin/tool"); err != nil {
		t.Errorf("plugin not installed: %v", err)
	}
	if code, _ := h.post("/api/install", map[string]any{"uploads": []string{"gone"}, "confirm": true}); code != 400 {
		t.Errorf("unknown upload: %d", code)
	}
}

func TestCatalogRowsShowWhatIsOnTheDeviceAndWhatHasAnUpdate(t *testing.T) {
	h := newHarness(t)
	syn := h.fd.cfg().SynthsDir
	for _, f := range []string{"me - VST - Acid", "me - VST - Dexed"} { // plugin folders (a plugin-meta.xml makes it one)
		os.MkdirAll(syn+"/"+f, 0o755)
		os.WriteFile(syn+"/"+f+"/plugin-meta.xml", []byte(`<PLUGIN name="x" file="%payload-path%/`+f+`/x.so" uid="0000000`+f[len(f)-1:]+`"/>`), 0o644)
	}
	// Acid is recorded at 1.0.0; Dexed was installed by hand: no recorded version
	os.WriteFile(syn+"/.mpc-store", []byte("acid\t1.0.0\tme - VST - Acid\t1\n"), 0o644)
	h.app.cat = []CatPlugin{
		{ID: "acid", Name: "Acid", Version: "1.1.0", Skin: "me - VST - Acid"},
		{ID: "dexed", Name: "Dexed", Version: "1.0.0", Skin: "me - VST - Dexed"},
		{ID: "nam", Name: "NAM", Version: "1.0.0", Skin: "me - VST - NAM"},
	}
	h.app.catAt = time.Now()
	if code, _ := h.post("/api/connect", map[string]string{"host": "127.0.0.1", "password": "secret"}); code != 200 {
		t.Fatal("connect")
	}
	_, b := h.do("GET", "/api/catalog", nil, nil)
	var out struct {
		Plugins []struct {
			ID               string
			Installed        bool
			InstalledVersion string
			Update           bool
		}
	}
	json.Unmarshal(b, &out)
	got := map[string][3]any{}
	for _, p := range out.Plugins {
		got[p.ID] = [3]any{p.Installed, p.InstalledVersion, p.Update}
	}
	want := map[string][3]any{"acid": {true, "1.0.0", true}, "dexed": {true, "", false}, "nam": {false, "", false}}
	for id, w := range want {
		if got[id] != w {
			t.Errorf("%s: got %v, want %v", id, got[id], w)
		}
	}
}

func jsonUnmarshal(b []byte, v any) { json.Unmarshal(b, v) }
