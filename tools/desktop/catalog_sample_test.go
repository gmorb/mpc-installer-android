package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The open-mpc catalog: device-limited entries (MPC Sample), the Sample's verified tier, aarch64-only versions, the VST2 folder name.
func TestCatalogMPCSampleFields(t *testing.T) {
	data := []byte(`{"schema":1,"plugins":[
 {"id":"sample-rack","name":"MPC Sample Rack","author":"x","kind":"addin","devices":["mpc-sample"],"verified_on":[],"distribution":"release","latest":"1.0.0",
  "versions":[{"version":"1.0.0","channel":"stable","gen2_only":true,"size":10,"sha256":"` + strings.Repeat("a", 64) + `","url":"https://e.com/r-aarch64.zip",
   "assets":{"aarch64":{"size":10,"sha256":"` + strings.Repeat("a", 64) + `","url":"https://e.com/r-aarch64.zip"}},"manifest":{}}]},
 {"id":"tr-9w9","name":"9W9","author":"y","kind":"instrument","verified_on":["gen1","sample"],"distribution":"release","latest":"2.0.0",
  "versions":[{"version":"2.0.0","channel":"stable","size":5,"sha256":"` + strings.Repeat("b", 64) + `","url":"https://e.com/9.zip","vst2":{"folder":"athousanddetails - VST - 9W9"},
   "assets":{"aarch64":{"size":6,"sha256":"` + strings.Repeat("c", 64) + `","url":"https://e.com/9-aarch64.zip"}},"manifest":{"skin":"athousanddetails - VST - 9W9"}}]}]}`)
	cat, err := parseCatalog(data)
	if err != nil || len(cat) != 2 {
		t.Fatal(err, cat)
	}
	rack, nine := cat[1], cat[0] // sorted by name: "9W9" first
	if len(rack.Devices) != 1 || rack.Devices[0] != "mpc-sample" || !rack.Gen2Only {
		t.Fatalf("rack: %+v", rack)
	}
	if _, err := rack.ForDevice("aarch64", ""); err == nil || !strings.Contains(err.Error(), "only for the MPC Sample") {
		t.Errorf("a Sample-only addin on a Live III must be refused: %v", err)
	}
	if c, err := rack.ForDevice("aarch64", "mpc-sample"); err != nil || c.URL != "https://e.com/r-aarch64.zip" {
		t.Errorf("on the Sample: %+v %v", c, err)
	}
	if nine.VST2Folder != "athousanddetails - VST - 9W9" || strings.Join(nine.VerifiedOn, ",") != "gen1,sample" {
		t.Errorf("9W9: %+v", nine)
	}
	if c, err := nine.ForDevice("aarch64", "mpc-sample"); err != nil || c.URL != "https://e.com/9-aarch64.zip" {
		t.Errorf("an every-device plugin on the Sample takes the aarch64 zip: %+v %v", c, err)
	}
}

func TestSampleIsDetectedBySettingsFile(t *testing.T) {
	fd := newFakeDevice(t)
	cfg := fd.cfg()
	writeSettings(fd, nil)
	d, err := Dial("127.0.0.1", "secret", cfg)
	if err != nil {
		t.Fatal(err)
	}
	if d.Info.Device != "" {
		t.Errorf("an MPC: %q", d.Info.Device)
	}
	d.Close()
	// the MPC Sample keeps its settings in "MPC Sample/MPC Sample.settings"
	b, _ := os.ReadFile(filepath.Join(fd.dir, "Settings", "MPC", "MPC.settings"))
	os.Remove(filepath.Join(fd.dir, "Settings", "MPC", "MPC.settings"))
	os.MkdirAll(filepath.Join(fd.dir, "Settings", "MPC", "MPC Sample"), 0o755)
	os.WriteFile(filepath.Join(fd.dir, "Settings", "MPC", "MPC Sample", "MPC Sample.settings"), b, 0o644)
	cfg.SettingsGlob += " " + filepath.Join(fd.dir, "Settings", "*", "MPC?Sample", "MPC?Sample.settings")
	d, err = Dial("127.0.0.1", "secret", cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if d.Info.Device != "mpc-sample" || !strings.HasSuffix(d.Info.Settings, "MPC Sample.settings") {
		t.Errorf("the Sample: %q %q", d.Info.Device, d.Info.Settings)
	}
}
