package rulemma

import (
	"bufio"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"
)

// GramInfo holds grammatical gender and number for a Russian noun / product type.
type GramInfo struct {
	Gender string `json:"gender"` // "masc", "femn", "neut", or "" (plural / indeclinable)
	Number string `json:"number"` // "sing" or "plur"
}

// Options configures Lemmatizer behavior.
type Options struct {
	// DisableSuffixFallback turns off heuristic suffix stripping for unseen Cyrillic OOV words.
	DisableSuffixFallback bool
	// ExtraOverridesPath optionally loads an additional user TSV override file (word\tlemma) on top of data/overrides.tsv.
	ExtraOverridesPath string
}

// Manifest describes dictionary schema, version, entry counts, and SHA-256 checksums (`data/manifest.json`).
type Manifest struct {
	SchemaVersion    int               `json:"schema_version"`
	DictVersion      string            `json:"dict_version"`
	EngineMinVersion string            `json:"engine_min_version"`
	Files            map[string]string `json:"files"`
	Counts           map[string]int    `json:"counts"`
	SHA256           map[string]string `json:"sha256"`
}

// LoadManifest reads data/manifest.json from dataDir.
func LoadManifest(dataDir string) (*Manifest, error) {
	return LoadManifestFS(os.DirFS(dataDir))
}

// LoadManifestFS reads manifest.json from an fs.FS (e.g., os.DirFS or embedded data.FS).
func LoadManifestFS(fsys fs.FS) (*Manifest, error) {
	b, err := fs.ReadFile(fsys, "manifest.json")
	if err != nil {
		return nil, fmt.Errorf("read manifest.json: %w", err)
	}
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("decode manifest.json: %w", err)
	}
	return &m, nil
}

// openMaybeGzip opens a plain or .gz file transparently from the OS filesystem.
func openMaybeGzip(path string) (io.ReadCloser, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	return wrapMaybeGzip(f, path)
}

// openMaybeGzipFS opens a plain or .gz file transparently from an fs.FS.
func openMaybeGzipFS(fsys fs.FS, name string) (io.ReadCloser, error) {
	f, err := fsys.Open(name)
	if err != nil {
		return nil, err
	}
	return wrapMaybeGzip(f, name)
}

func wrapMaybeGzip(f io.ReadCloser, name string) (io.ReadCloser, error) {
	if strings.HasSuffix(name, ".gz") {
		gz, err := gzip.NewReader(f)
		if err != nil {
			f.Close()
			return nil, err
		}
		return &gzipReadCloser{gz: gz, f: f}, nil
	}
	return f, nil
}

type gzipReadCloser struct {
	gz *gzip.Reader
	f  io.ReadCloser
}

func (g *gzipReadCloser) Read(p []byte) (int, error) { return g.gz.Read(p) }
func (g *gzipReadCloser) Close() error {
	err1 := g.gz.Close()
	err2 := g.f.Close()
	if err1 != nil {
		return err1
	}
	return err2
}

func firstExistingFS(fsys fs.FS, names ...string) string {
	for _, n := range names {
		if _, err := fs.Stat(fsys, n); err == nil {
			return n
		}
	}
	return ""
}

func loadLemmasFS(fsys fs.FS, label string) (map[string]string, error) {
	name := firstExistingFS(fsys, "lemmas.tsv.gz", "lemmas.tsv", "lemmas.json.gz", "lemmas.json")
	if name == "" {
		return nil, fmt.Errorf("lemmas dictionary not found in %s", label)
	}
	rc, err := openMaybeGzipFS(fsys, name)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", name, err)
	}
	defer rc.Close()

	if strings.Contains(name, ".json") {
		var m map[string]string
		if err := json.NewDecoder(rc).Decode(&m); err != nil {
			return nil, fmt.Errorf("decode %s: %w", name, err)
		}
		out := make(map[string]string, len(m))
		for k, v := range m {
			out[strings.ToLower(k)] = normYo(strings.ToLower(v))
		}
		return out, nil
	}

	out := make(map[string]string, 420000)
	sc := bufio.NewScanner(rc)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if line == "" || line[0] == '#' {
			continue
		}
		tab := strings.IndexByte(line, '\t')
		if tab <= 0 {
			continue
		}
		w := strings.ToLower(line[:tab])
		rest := line[tab+1:]
		if tab2 := strings.IndexByte(rest, '\t'); tab2 >= 0 {
			rest = rest[:tab2]
		}
		out[w] = normYo(strings.ToLower(rest))
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("scan %s: %w", name, err)
	}
	return out, nil
}

func applyOverridesReader(lemmas map[string]string, r io.Reader) (int, error) {
	n := 0
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 32*1024), 512*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || line[0] == '#' {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) < 2 {
			continue
		}
		w := strings.ToLower(strings.TrimSpace(parts[0]))
		l := normYo(strings.ToLower(strings.TrimSpace(parts[1])))
		if w == "" || l == "" {
			continue
		}
		lemmas[w] = l
		if wn := normYo(w); wn != w {
			lemmas[wn] = l
		}
		n++
	}
	return n, sc.Err()
}

func applyOverrides(lemmas map[string]string, path string) (int, error) {
	rc, err := openMaybeGzip(path)
	if err != nil {
		return 0, err
	}
	defer rc.Close()
	return applyOverridesReader(lemmas, rc)
}

func applyOverridesFS(lemmas map[string]string, fsys fs.FS, name string) (int, error) {
	rc, err := openMaybeGzipFS(fsys, name)
	if err != nil {
		return 0, err
	}
	defer rc.Close()
	return applyOverridesReader(lemmas, rc)
}

func loadAdjFormsFS(fsys fs.FS) (map[string]string, error) {
	name := firstExistingFS(fsys, "adj_forms.tsv.gz", "adj_forms.tsv", "adj_forms.json.gz", "adj_forms.json")
	if name == "" {
		return map[string]string{}, nil
	}
	rc, err := openMaybeGzipFS(fsys, name)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", name, err)
	}
	defer rc.Close()

	if strings.Contains(name, ".json") {
		var m map[string]string
		if err := json.NewDecoder(rc).Decode(&m); err != nil {
			return nil, fmt.Errorf("decode %s: %w", name, err)
		}
		return m, nil
	}

	out := make(map[string]string, 75000)
	sc := bufio.NewScanner(rc)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if line == "" || line[0] == '#' {
			continue
		}
		tab := strings.IndexByte(line, '\t')
		if tab <= 0 {
			continue
		}
		out[line[:tab]] = line[tab+1:]
	}
	return out, sc.Err()
}

func loadTypesFS(fsys fs.FS) (map[string]GramInfo, error) {
	name := firstExistingFS(fsys, "types.tsv.gz", "types.tsv", "types.json.gz", "types.json")
	if name == "" {
		return map[string]GramInfo{}, nil
	}
	rc, err := openMaybeGzipFS(fsys, name)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", name, err)
	}
	defer rc.Close()

	if strings.Contains(name, ".json") {
		var m map[string]GramInfo
		if err := json.NewDecoder(rc).Decode(&m); err != nil {
			return nil, fmt.Errorf("decode %s: %w", name, err)
		}
		out := make(map[string]GramInfo, len(m))
		for k, v := range m {
			out[strings.ToLower(k)] = v
		}
		return out, nil
	}

	out := make(map[string]GramInfo, 5000)
	sc := bufio.NewScanner(rc)
	for sc.Scan() {
		line := sc.Text()
		if line == "" || line[0] == '#' {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) >= 3 {
			out[strings.ToLower(parts[0])] = GramInfo{Gender: parts[1], Number: parts[2]}
		}
	}
	return out, sc.Err()
}

func loadAdjNounFS(fsys fs.FS) (map[string]string, error) {
	name := firstExistingFS(fsys, "adj_noun.tsv.gz", "adj_noun.tsv")
	if name == "" {
		return map[string]string{}, nil
	}
	rc, err := openMaybeGzipFS(fsys, name)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", name, err)
	}
	defer rc.Close()

	out := make(map[string]string, 4500)
	sc := bufio.NewScanner(rc)
	for sc.Scan() {
		line := sc.Text()
		if line == "" || line[0] == '#' {
			continue
		}
		tab := strings.IndexByte(line, '\t')
		if tab <= 0 {
			continue
		}
		adj := normYo(strings.ToLower(strings.TrimSpace(line[:tab])))
		noun := normYo(strings.ToLower(strings.TrimSpace(line[tab+1:])))
		if adj != "" && noun != "" {
			out[adj] = noun
		}
	}
	return out, sc.Err()
}
