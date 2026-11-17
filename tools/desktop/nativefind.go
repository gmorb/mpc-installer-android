package main

// Finding plugins in MPC OS 3.10.1's format (inMusic's MPC Plugin Builder SDK) in a zip that is not laid out exactly the way the
// MPC needs. A plugin is one folder "<Manufacturer> - VST - <Name>" holding a plugin.json and the binaries it names, and that
// folder goes straight into VST2 Plugins. Zips people pass around are often a little off: the folder sits inside another folder
// (a release folder, "VST2 Plugins", a project's build folder), the plugin's files are zipped without their folder, the folder has
// another name, a Mac added __MACOSX and .DS_Store, or plugin.json names a binary at a path where it is not. When the files
// themselves are right, the app puts them in the right shape and says what it changed.

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
)

// nativeRoot is one plugin found in a zip.
type nativeRoot struct {
	Dir     string            // the zip folder holding its plugin.json ("" for the zip's top)
	Folder  string            // its folder name in VST2 Plugins: "<Manufacturer> - VST - <Name>"
	Name    string            // from plugin.json
	Version string            // from plugin.json
	Bins    map[string]string // "file32", "file64" or "file": the binary's path inside the folder
	PJ      []byte            // plugin.json to write instead of the zip's (nil: the zip's own is right)
	Fixes   []string          // what was changed to make it install, for the log
}

func zjoin(dir, rel string) string {
	if dir == "" {
		return rel
	}
	return dir + "/" + rel
}

func under(dir, n string) bool { return dir == "" || strings.HasPrefix(n, dir+"/") }

// findNative looks through the whole zip for plugins in the SDK's format. A folder whose plugin.json names binaries that are all
// there is taken as it is. Only when there is none, a plugin.json whose binaries are elsewhere in its folder is repaired: each
// binary is found by its file name, or else by the processor it is built for (32-bit or 64-bit ARM).
func findNative(files []*zip.File) ([]nativeRoot, error) {
	names := map[string]*zip.File{}
	for _, f := range files {
		if n, err := cleanName(f.Name); err == nil && !junk(n) && !f.FileInfo().IsDir() {
			names[n] = f
		}
	}
	type cand struct {
		dir    string
		pj     pluginJSON
		raw    []byte
		strict bool
	}
	var cands []cand
	for n, f := range names {
		if path.Base(n) != "plugin.json" {
			continue
		}
		b, err := readZipFile(f, 1<<20)
		if err != nil {
			continue
		}
		var pj pluginJSON
		var any map[string]json.RawMessage
		if json.Unmarshal(b, &pj) != nil || json.Unmarshal(b, &any) != nil || strings.TrimSpace(pj.Name) == "" {
			continue
		}
		_, hasUID := any["uid"]
		if pj.Format == "" && pj.File == "" && pj.File32 == "" && pj.File64 == "" && !hasUID {
			continue // some other program's plugin.json
		}
		dir := ""
		if i := strings.LastIndex(n, "/"); i >= 0 {
			dir = n[:i]
		}
		c := cand{dir: dir, pj: pj, raw: b, strict: true}
		named := 0
		for _, v := range []string{pj.File32, pj.File64, pj.File} {
			if v == "" {
				continue
			}
			named++
			if cn, err := cleanName(v); err != nil || names[zjoin(dir, cn)] == nil {
				c.strict = false
			}
		}
		c.strict = c.strict && named > 0
		cands = append(cands, c)
	}
	if len(cands) == 0 {
		return nil, nil
	}
	nests := func(a, b string) bool {
		return a == "" || b == "" || a == b || strings.HasPrefix(b, a+"/") || strings.HasPrefix(a, b+"/")
	}
	// a plugin.json that would need repairing gives way to an intact one it contains or sits in (a project folder holding its
	// own build output, say)
	beaten := func(c cand) bool {
		if c.strict {
			return false
		}
		for _, s := range cands {
			if s.strict && nests(s.dir, c.dir) {
				return true
			}
		}
		return false
	}
	// the outermost of nested plugin.json files wins (one inside a plugin's Content is the plugin's data)
	sort.Slice(cands, func(i, j int) bool {
		if a, b := strings.Count(cands[i].dir, "/")+btoi(cands[i].dir != ""), strings.Count(cands[j].dir, "/")+btoi(cands[j].dir != ""); a != b {
			return a < b
		}
		return cands[i].dir < cands[j].dir
	})
	var out []nativeRoot
	taken := map[string]string{}
	for _, c := range cands {
		if beaten(c) {
			continue
		}
		inside := false
		for _, r := range out {
			inside = inside || (r.Dir == "" || strings.HasPrefix(c.dir, r.Dir+"/"))
		}
		if inside {
			continue
		}
		r := nativeRoot{Dir: c.dir, Name: c.pj.Name, Version: c.pj.Version, Bins: map[string]string{}}
		set := map[string]string{}
		if c.strict {
			for k, v := range map[string]string{"file32": c.pj.File32, "file64": c.pj.File64, "file": c.pj.File} {
				if v != "" {
					cn, _ := cleanName(v)
					r.Bins[k] = cn
				}
			}
		} else if err := repairBins(names, c.dir, c.pj, &r, set); err != nil {
			return nil, fmt.Errorf("%s: %w", c.pj.Name, err)
		}
		want := legalFolderName(strings.TrimSpace(c.pj.Manufacturer) + " - VST - " + strings.TrimSpace(c.pj.Name))
		if strings.TrimSpace(c.pj.Manufacturer) == "" {
			want = legalFolderName("VST - " + strings.TrimSpace(c.pj.Name))
		}
		if len(want) >= 128 {
			return nil, fmt.Errorf("%s: the folder name %q is too long for MPC", c.pj.Name, want)
		}
		r.Folder = want
		switch base := path.Base(c.dir); {
		case c.dir == "":
			r.Fixes = append(r.Fixes, fmt.Sprintf("its files were not in a folder: they go in %q", want))
		case base != want:
			r.Fixes = append(r.Fixes, fmt.Sprintf("its folder %q is renamed %q, the name MPC needs", base, want))
		}
		if c.dir != "" && strings.Contains(c.dir, "/") {
			r.Fixes = append(r.Fixes, fmt.Sprintf("found inside %q in the zip; the rest of the zip is left out", path.Dir(c.dir)+"/"))
		}
		addFormat := c.pj.Format == ""
		if len(set) > 0 || addFormat {
			pj, err := editPluginJSON(c.raw, set, addFormat)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", c.pj.Name, err)
			}
			r.PJ = pj
			if addFormat {
				r.Fixes = append(r.Fixes, `added "format": "vst2" to plugin.json (the SDK now requires it)`)
			}
		}
		if other, ok := taken[want]; ok {
			return nil, fmt.Errorf("the zip has two copies of %s (%s/ and %s/): keep one", want, orTop(other), orTop(c.dir))
		}
		taken[want] = c.dir
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Folder < out[j].Folder })
	return out, nil
}

func bits(key string) string {
	return map[string]string{"file32": "32-bit", "file64": "64-bit"}[key]
}

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}

func orTop(dir string) string {
	if dir == "" {
		return "the top of the zip"
	}
	return dir
}

// repairBins finds the binaries of a plugin whose plugin.json names paths that are not in the zip, records them in r.Bins and the
// corrected plugin.json values in set.
func repairBins(names map[string]*zip.File, dir string, pj pluginJSON, r *nativeRoot, set map[string]string) error {
	byArch := map[string][]string{} // "arm32", "arm64": ELF libraries inside the folder
	byBase := map[string][]string{}
	for n, f := range names {
		if !under(dir, n) {
			continue
		}
		rel := strings.TrimPrefix(n, dir+"/")
		if dir == "" {
			rel = n
		}
		head, err := readZipFile(f, 20)
		if err != nil || len(head) < 20 || string(head[:4]) != "\x7fELF" {
			continue
		}
		if a, err := elfArch(head); err == nil {
			byArch[a] = append(byArch[a], rel)
			byBase[path.Base(rel)] = append(byBase[path.Base(rel)], rel)
		}
	}
	find := func(key, named, arch string) {
		if named != "" {
			if cn, err := cleanName(named); err == nil && names[zjoin(dir, cn)] != nil {
				r.Bins[key] = cn
				return
			}
			if m := byBase[path.Base(named)]; len(m) == 1 {
				r.Bins[key], set[key] = m[0], m[0]
				r.Fixes = append(r.Fixes, fmt.Sprintf("plugin.json pointed to the wrong place for its %s build: now %q", bits(key), m[0]))
				return
			}
		}
		if arch == "" {
			return
		}
		if m := byArch[arch]; len(m) == 1 {
			r.Bins[key], set[key] = m[0], m[0]
			if named != "" {
				r.Fixes = append(r.Fixes, fmt.Sprintf("plugin.json named a %s build that is not in the zip (%q): the folder's only %s library, %q, is used", bits(key), named, bits(key), m[0]))
			} else {
				r.Fixes = append(r.Fixes, fmt.Sprintf("plugin.json named no %s build: the folder's only one, %q, is used", bits(key), m[0]))
			}
		}
	}
	if pj.File32 == "" && pj.File64 == "" && pj.File != "" {
		find("file", pj.File, "")
		if r.Bins["file"] == "" { // one binary for both, not found by name: whichever single library there is
			if len(byArch["arm32"])+len(byArch["arm64"]) == 1 {
				m := append(byArch["arm32"], byArch["arm64"]...)[0]
				r.Bins["file"], set["file"] = m, m
				r.Fixes = append(r.Fixes, fmt.Sprintf("plugin.json named its library %q, which is not in the zip; the folder's only library, %q, is used", pj.File, m))
			}
		}
	} else {
		find("file32", pj.File32, "arm32")
		find("file64", pj.File64, "arm64")
	}
	if len(r.Bins) == 0 {
		var want []string
		for _, v := range []string{pj.File32, pj.File64, pj.File} {
			if v != "" {
				want = append(want, v)
			}
		}
		if len(want) == 0 {
			return errors.New("its plugin.json names no binary and no ARM library was found beside it")
		}
		return fmt.Errorf("the binaries its plugin.json names (%s) are not in the zip, and no ARM library was found beside it to use instead", strings.Join(want, ", "))
	}
	return nil
}

// editPluginJSON changes keys of a plugin.json, keeping its other keys and their order. A key it did not have goes after "version"
// for "format", else after the last file key or at the end.
func editPluginJSON(raw []byte, set map[string]string, addFormat bool) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return nil, errors.New("plugin.json is not a JSON object")
	}
	type kv struct {
		k string
		v json.RawMessage
	}
	var kvs []kv
	for dec.More() {
		t, err := dec.Token()
		if err != nil {
			return nil, fmt.Errorf("bad plugin.json: %w", err)
		}
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, fmt.Errorf("bad plugin.json: %w", err)
		}
		kvs = append(kvs, kv{t.(string), v})
	}
	str := func(s string) json.RawMessage { b, _ := json.Marshal(s); return b }
	insertAfter := func(after []string, k string, v json.RawMessage) {
		at := len(kvs)
		for i, e := range kvs {
			for _, a := range after {
				if e.k == a {
					at = i + 1
				}
			}
		}
		kvs = append(kvs[:at], append([]kv{{k, v}}, kvs[at:]...)...)
	}
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		found := false
		for i := range kvs {
			if kvs[i].k == k {
				kvs[i].v, found = str(set[k]), true
			}
		}
		if !found {
			insertAfter([]string{"numOutputs", "file", "file32", "file64"}, k, str(set[k]))
		}
	}
	if addFormat {
		insertAfter([]string{"version"}, "format", str("vst2"))
	}
	var b bytes.Buffer
	b.WriteString("{\n")
	for i, e := range kvs {
		var c bytes.Buffer
		if err := json.Compact(&c, e.v); err != nil {
			return nil, err
		}
		k, _ := json.Marshal(e.k)
		fmt.Fprintf(&b, "  %s: %s", k, c.Bytes())
		if i < len(kvs)-1 {
			b.WriteString(",")
		}
		b.WriteString("\n")
	}
	b.WriteString("}\n")
	return b.Bytes(), nil
}
