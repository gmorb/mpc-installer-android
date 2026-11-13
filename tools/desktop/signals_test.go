package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSignalsAreReadAndCached(t *testing.T) {
	hits := 0
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		switch r.URL.Path {
		case "/api/counts":
			w.Write([]byte(`{"counts":{"acid":{"working":12,"likes":5,"versions":{"2.1.0":4}},"tr-9w9":{"working":3,"likes":0,"sample":2,"versions":{}}}}`))
		case "/api/plugin":
			w.Write([]byte(`{"id":"` + r.URL.Query().Get("id") + `","working":12,"likes":5,"versions":{},"devices":[{"device":"MPC One","os":"3.10.1","n":3}],"recent":[]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	oldBase, oldClient := signalsBase, signalsClient
	signalsBase, signalsClient = srv.URL, srv.Client()
	defer func() { signalsBase, signalsClient = oldBase, oldClient }()
	h := newHarness(t)
	_, b := h.do("GET", "/api/signals", nil, nil)
	if !strings.Contains(string(b), `"ok":true`) || !strings.Contains(string(b), `"acid":{"working":12,"likes":5`) || !strings.Contains(string(b), `"sample":2`) {
		t.Fatalf("counts: %s", b)
	}
	h.do("GET", "/api/signals", nil, nil)
	if hits != 1 {
		t.Errorf("counts are cached: %d requests", hits)
	}
	_, b = h.do("GET", "/api/signals?id=acid", nil, nil)
	if !strings.Contains(string(b), `"MPC One"`) {
		t.Fatalf("detail: %s", b)
	}
	_, b = h.do("GET", "/api/signals?id=../etc", nil, nil)
	if !strings.Contains(string(b), `"ok":false`) || hits != 2 {
		t.Errorf("a bad id is refused without asking the service: %s (%d)", b, hits)
	}
}

func TestSignalsDownIsQuiet(t *testing.T) {
	oldBase := signalsBase
	signalsBase = "https://127.0.0.1:1"
	defer func() { signalsBase = oldBase }()
	h := newHarness(t)
	resp, b := h.do("GET", "/api/signals", nil, nil)
	if resp.StatusCode != 200 || !strings.Contains(string(b), `"ok":false`) {
		t.Fatalf("the page goes on without counts: %d %s", resp.StatusCode, b)
	}
}
