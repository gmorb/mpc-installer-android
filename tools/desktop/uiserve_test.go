package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
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
	_, cfg, _, _ := nativeDevice(t)
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
