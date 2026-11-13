package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// TestUIServe runs the real app against a simulated MPC on MPC OS 3.10.1 (a USB drive with no VST2 Plugins folder yet, internal
// storage) with a two-plugin catalog served locally, for checking the page in a browser (ui_test/ui_vst2.py). It only runs with
// MPC_UI_SERVE=<port>, and serves until MPC_UI_SECONDS (default 120) have passed.
func TestUIServe(t *testing.T) {
	port := os.Getenv("MPC_UI_SERVE")
	if port == "" {
		t.Skip("set MPC_UI_SERVE=<port> to serve the page against a simulated MPC")
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	fd, cfg, _, _ := nativeDevice(t)
	if os.Getenv("MPC_UI_OLD") != "" { // two plugins installed the old way, in the internal Synths folder
		acid := oldPlugin(t, fd, cfg.SynthsDir, "Acid", "41634964", 0x28)
		bass := oldPlugin(t, fd, cfg.SynthsDir, "Bass", "42617373", 0x28)
		writeSettings(fd, []string{cfg.SynthsDir}, entry("Acid", acid+"/lib/acid.so", "41634964"), entry("Bass", bass+"/lib/bass.so", "42617373"))
	}
	zips := map[string][]byte{}
	var cat []CatPlugin
	for _, c := range []struct{ id, name, skin string }{{"acid", "Acid", "Acme - VST - Acid"}, {"wobble", "Wobble", "Acme - VST - Wobble"}} {
		b, _ := os.ReadFile(catalogPkg(t, 0x28, c.skin).Path)
		zips[c.id] = b
		sum := sha256.Sum256(b)
		cat = append(cat, CatPlugin{ID: c.id, Name: c.name, Author: "Acme", Kind: "instrument", Summary: "A test plugin.", Version: "2.1.0", Size: int64(len(b)), SHA256: hex.EncodeToString(sum[:]), Skin: c.skin})
	}
	files := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(zips[r.URL.Path[1:]]) }))
	defer files.Close()
	old := httpClient
	httpClient = files.Client()
	defer func() { httpClient = old }()
	for i := range cat {
		cat[i].URL = files.URL + "/" + cat[i].ID
	}
	cat[1].VerifiedOn = []string{"gen1", "sample"} // Wobble: verified on an MPC Sample
	cat = append(cat, CatPlugin{ID: "sample-rack", Name: "MPC Sample Rack", Author: "Someone", Kind: "addin", Summary: "A rack page for the MPC Sample's screen.",
		Version: "1.0.0", Size: 300000, SHA256: strings.Repeat("a", 64), URL: files.URL + "/none", Devices: []string{"mpc-sample"}, Gen2: true, Gen2Only: true})
	// catalog details and community counts, as the catalog and its signals service give them
	cat[0].Summary = "A squelchy 303-style bass line synth with an 18 dB ladder filter, accent and slide per step, a built-in distortion stage and a tempo-synced delay. Comes with 64 presets made for MPC drum programs, and runs comfortably on a Gen1 MPC."
	cat[0].Downloads, cat[0].License, cat[0].Repo, cat[0].Tier = 1840, "GPL-3.0", "acme/acid", "verified"
	cat[1].Downloads, cat[1].License, cat[1].Repo, cat[1].Featured = 420, "MIT", "acme/wobble", true
	cat[2].Downloads = 95
	sig := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/plugin" {
			fmt.Fprintf(w, `{"id":%q,"working":12,"likes":31,"devices":[{"device":"MPC One+","os":"3.10.1","n":5},{"device":"MPC Live II","os":"3.9.0","n":4},{"device":"Force","os":"3.10.1","n":3}],"recent":[]}`, r.URL.Query().Get("id"))
			return
		}
		w.Write([]byte(`{"counts":{"acid":{"working":12,"likes":31,"versions":{"2.1.0":9,"2.0.0":3}},"wobble":{"working":4,"likes":58,"versions":{"2.1.0":4},"sample":2}}}`))
	}))
	defer sig.Close()
	oldBase, oldClient := signalsBase, signalsClient
	signalsBase, signalsClient = sig.URL, sig.Client()
	defer func() { signalsBase, signalsClient = oldBase, oldClient }()
	if shot := os.Getenv("MPC_UI_SHOT"); shot != "" {
		for i := range cat {
			cat[i].Screenshot = shot
		}
	}
	ln, err := net.Listen("tcp", "127.0.0.1:"+port)
	if err != nil {
		t.Fatal(err)
	}
	app := NewApp(cfg, "tok", ln.Addr().String(), "https://127.0.0.1:1/none.json", t.TempDir())
	app.cat, app.catAt = cat, time.Now().Add(time.Hour)
	srv := &http.Server{Handler: app.Handler()}
	go srv.Serve(ln)
	defer srv.Close()
	fmt.Printf("UI at http://%s/?t=tok  (MPC at 127.0.0.1, password secret)\n", ln.Addr())
	secs := 120
	fmt.Sscanf(os.Getenv("MPC_UI_SECONDS"), "%d", &secs)
	time.Sleep(time.Duration(secs) * time.Second)
}
