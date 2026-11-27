package rulemma

import (
	"bufio"
	"bytes"
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
	// PreloadAll eagerly loads optional v0.3 morphology (morph/alt_lemmas) and inflection (paradigms) tables
	// during Load instead of lazy on-first-use loading.
	PreloadAll bool
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
		br := bufio.NewReaderSize(f, 32*1024)
		gz, err := gzip.NewReader(br)
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

func trimCR(b []byte) []byte {
	if len(b) > 0 && b[len(b)-1] == '\r' {
		return b[:len(b)-1]
	}
	return b
}

func loadLemmasFS(fsys fs.FS, label string) (*compactStrMap, error) {
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
		out := newCompactStrMap(len(m), len(m)*20+64)
		for k, v := range m {
			out.putString(strings.ToLower(k), normYo(strings.ToLower(v)), true)
		}
		return out, nil
	}

	out := newCompactStrMap(215000, 4200*1024)
	sc := bufio.NewScanner(rc)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	var scratchK, scratchV []byte
	for sc.Scan() {
		line := trimCR(sc.Bytes())
		if len(line) == 0 || line[0] == '#' {
			continue
		}
		tab := bytes.IndexByte(line, '\t')
		if tab <= 0 {
			continue
		}
		rawK := line[:tab]
		rest := line[tab+1:]
		if tab2 := bytes.IndexByte(rest, '\t'); tab2 >= 0 {
			rest = rest[:tab2]
		}
		w := normalizeLowerBytes(rawK, scratchK)
		l := normalizeLowerNoYoBytes(rest, scratchV)
		out.putBytes(w, l, true)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("scan %s: %w", name, err)
	}
	return out, nil
}

func applyOverridesReader(lemmas *compactStrMap, overrides map[string]struct{}, r io.Reader) (int, error) {
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
		lemmas.putString(w, l, false)
		if overrides != nil {
			overrides[w] = struct{}{}
		}
		if wn := normYo(w); wn != w {
			lemmas.putString(wn, l, false)
			if overrides != nil {
				overrides[wn] = struct{}{}
			}
		}
		n++
	}
	return n, sc.Err()
}

func applyOverrides(lemmas *compactStrMap, overrides map[string]struct{}, path string) (int, error) {
	rc, err := openMaybeGzip(path)
	if err != nil {
		return 0, err
	}
	defer rc.Close()
	return applyOverridesReader(lemmas, overrides, rc)
}

func applyOverridesFS(lemmas *compactStrMap, overrides map[string]struct{}, fsys fs.FS, name string) (int, error) {
	rc, err := openMaybeGzipFS(fsys, name)
	if err != nil {
		return 0, err
	}
	defer rc.Close()
	return applyOverridesReader(lemmas, overrides, rc)
}

func loadAdjFormsFS(fsys fs.FS) (*adjFormsTable, error) {
	name := firstExistingFS(fsys, "adj_forms.tsv.gz", "adj_forms.tsv", "adj_forms.json.gz", "adj_forms.json")
	if name == "" {
		return nil, nil
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
		out := newAdjFormsTable(len(m)/4+16, len(m)*24+64)
		for k, v := range m {
			out.putComposite([]byte(k), []byte(v))
		}
		out.freeze()
		return out, nil
	}

	out := newAdjFormsTable(19000, 1800*1024)
	sc := bufio.NewScanner(rc)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		line := trimCR(sc.Bytes())
		if len(line) == 0 || line[0] == '#' {
			continue
		}
		tab := bytes.IndexByte(line, '\t')
		if tab <= 0 || tab+1 >= len(line) {
			continue
		}
		out.putComposite(line[:tab], line[tab+1:])
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	out.freeze()
	return out, nil
}

func internGenderBytes(b []byte) string {
	switch string(b) {
	case "masc":
		return "masc"
	case "femn":
		return "femn"
	case "neut":
		return "neut"
	case "":
		return ""
	default:
		return string(b)
	}
}

func internNumberBytes(b []byte) string {
	switch string(b) {
	case "sing":
		return "sing"
	case "plur":
		return "plur"
	case "":
		return ""
	default:
		return string(b)
	}
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

	out := make(map[string]GramInfo, 4800)
	sc := bufio.NewScanner(rc)
	sc.Buffer(make([]byte, 32*1024), 512*1024)
	var scratch []byte
	for sc.Scan() {
		line := trimCR(sc.Bytes())
		if len(line) == 0 || line[0] == '#' {
			continue
		}
		t1 := bytes.IndexByte(line, '\t')
		if t1 <= 0 || t1+1 >= len(line) {
			continue
		}
		rest := line[t1+1:]
		t2 := bytes.IndexByte(rest, '\t')
		if t2 < 0 {
			continue
		}
		numB := rest[t2+1:]
		if t3 := bytes.IndexByte(numB, '\t'); t3 >= 0 {
			numB = numB[:t3]
		}
		k := string(normalizeLowerBytes(line[:t1], scratch))
		out[k] = GramInfo{
			Gender: internGenderBytes(rest[:t2]),
			Number: internNumberBytes(numB),
		}
	}
	return out, sc.Err()
}

func loadAdjNounFS(fsys fs.FS) (*compactStrMap, error) {
	name := firstExistingFS(fsys, "adj_noun.tsv.gz", "adj_noun.tsv")
	if name == "" {
		return nil, nil
	}
	rc, err := openMaybeGzipFS(fsys, name)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", name, err)
	}
	defer rc.Close()

	out := newCompactStrMap(4100, 150*1024)
	sc := bufio.NewScanner(rc)
	sc.Buffer(make([]byte, 32*1024), 512*1024)
	var scratchK, scratchV []byte
	for sc.Scan() {
		line := trimCR(sc.Bytes())
		if len(line) == 0 || line[0] == '#' {
			continue
		}
		tab := bytes.IndexByte(line, '\t')
		if tab <= 0 {
			continue
		}
		adj := normalizeLowerNoYoBytes(bytes.TrimSpace(line[:tab]), scratchK)
		noun := normalizeLowerNoYoBytes(bytes.TrimSpace(line[tab+1:]), scratchV)
		if len(adj) > 0 && len(noun) > 0 {
			out.putBytes(adj, noun, false)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	out.freeze()
	return out, nil
}

func parseHexUint32Bytes(b []byte) (uint32, bool) {
	if len(b) == 0 || len(b) > 8 {
		return 0, false
	}
	var v uint32
	for _, c := range b {
		var d uint32
		switch {
		case c >= '0' && c <= '9':
			d = uint32(c - '0')
		case c >= 'a' && c <= 'f':
			d = uint32(c-'a') + 10
		case c >= 'A' && c <= 'F':
			d = uint32(c-'A') + 10
		default:
			return 0, false
		}
		v = (v << 4) | d
	}
	return v, true
}

func parseDecUint16Bytes(b []byte) (uint16, bool) {
	if len(b) == 0 {
		return 0, false
	}
	var v uint32
	for _, c := range b {
		if c < '0' || c > '9' {
			return 0, false
		}
		v = v*10 + uint32(c-'0')
		if v > 65535 {
			return 0, false
		}
	}
	return uint16(v), true
}
