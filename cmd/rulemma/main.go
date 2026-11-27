// Command rulemma is a fast, concurrent Russian & e-commerce lemmatizer CLI.
//
// Subcommands:
//
//	rulemma version
//	rulemma info      [--data data] [--overrides FILE]
//	rulemma lemmatize [--data data] [--mode text|key|semkey|both|all|norm] [--stopwords FILE|none|w1,w2] [--keep-yo] [--workers N]
//	rulemma tsv       --in IN.tsv --out OUT.tsv [--col 0] [--mode text|key|semkey|both|all|norm] [--stopwords FILE|none|w1,w2] [--keep-yo] [--header] [--data data] [--workers N]
//	rulemma bench     --in QUERIES.txt [--data data] [--mode text|key|semkey|both|all|norm] [--stopwords FILE|none|w1,w2] [--workers N]
//	rulemma seoname   --in PRODUCTS.csv --out RESULT.csv [--data data] [--workers N]
package main

import (
	"bufio"
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/DimaKorshikov/rulemma"
)

func defaultDataDir() string {
	if env := os.Getenv("RULEMMA_DATA"); env != "" {
		return env
	}
	if _, err := os.Stat("data/lemmas.tsv.gz"); err == nil {
		return "data"
	}
	if exe, err := os.Executable(); err == nil {
		cand := filepath.Join(filepath.Dir(exe), "..", "data")
		if _, err := os.Stat(filepath.Join(cand, "lemmas.tsv.gz")); err == nil {
			return cand
		}
		cand2 := filepath.Join(filepath.Dir(exe), "data")
		if _, err := os.Stat(filepath.Join(cand2, "lemmas.tsv.gz")); err == nil {
			return cand2
		}
	}
	return "data"
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}
	sub := os.Args[1]
	args := os.Args[2:]
	switch sub {
	case "version", "-v", "--version":
		fmt.Printf("rulemma %s (abi=%d, dict_schema=%d)\n", rulemma.Version, rulemma.ABIVersion, rulemma.DictSchemaVersion)
	case "info":
		cmdInfo(args)
	case "lemmatize":
		cmdLemmatize(args)
	case "tsv":
		cmdTSV(args)
	case "bench":
		cmdBench(args)
	case "seoname":
		cmdSEOName(args)
	case "analyze":
		cmdAnalyze(args)
	case "inflect":
		cmdInflect(args)
	case "-h", "--help", "help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand %q\n", sub)
		usage()
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `rulemma — fast dictionary-backed Russian & e-commerce lemmatizer (Go)

Usage:
  rulemma version
  rulemma info      [--data DIR] [--overrides FILE]
  rulemma lemmatize [--data DIR] [--overrides FILE] [--mode text|key|semkey|both|all|norm] [--profile default|technical] [--stopwords FILE|none|w1,w2] [--keep-yo] [--workers N]
  rulemma tsv       --in IN.tsv --out OUT.tsv [--col 0] [--mode text|key|semkey|both|all|norm] [--profile default|technical] [--stopwords FILE|none|w1,w2] [--keep-yo] [--header] [--data DIR] [--workers N]
  rulemma bench     --in FILE [--data DIR] [--overrides FILE] [--mode text|key|semkey|both|all|norm] [--profile default|technical] [--stopwords FILE|none|w1,w2] [--workers N]
  rulemma seoname   --in PRODUCTS.csv --out RESULT.csv [--data DIR] [--overrides FILE] [--workers N]
  rulemma analyze   [--data DIR] [--overrides FILE] [WORD ...]
  rulemma inflect   [--data DIR] [--overrides FILE] --case gent [--number sing|plur] [PHRASE ...]`)
}

func mustLoad(dataDir, overrides string) (*rulemma.Lemmatizer, time.Duration) {
	t0 := time.Now()
	lem, err := rulemma.LoadWithOptions(dataDir, rulemma.Options{ExtraOverridesPath: overrides})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error loading dictionaries from %s: %v\n", dataDir, err)
		os.Exit(1)
	}
	return lem, time.Since(t0)
}

func loadStopWordsFlag(spec string) (map[string]struct{}, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return rulemma.DefaultKeyStopWords, nil
	}
	if strings.EqualFold(spec, "none") {
		return map[string]struct{}{}, nil
	}
	if info, err := os.Stat(spec); err == nil && !info.IsDir() {
		f, err := os.Open(spec)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		var words []string
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" || line[0] == '#' {
				continue
			}
			words = append(words, line)
		}
		if err := sc.Err(); err != nil {
			return nil, err
		}
		return rulemma.MakeStopWords(words), nil
	}
	if strings.EqualFold(spec, "general") {
		return rulemma.GeneralKeyStopWords, nil
	}
	if strings.EqualFold(spec, "ecommerce") {
		return rulemma.EcommerceKeyStopWords, nil
	}
	return rulemma.MakeStopWords(strings.Split(spec, ",")), nil
}

func mustLoadStopWords(spec string) map[string]struct{} {
	stop, err := loadStopWordsFlag(spec)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error loading stopwords %q: %v\n", spec, err)
		os.Exit(1)
	}
	return stop
}

func cmdInfo(args []string) {
	fs := flag.NewFlagSet("info", flag.ExitOnError)
	dataDir := fs.String("data", defaultDataDir(), "Path to data directory")
	overrides := fs.String("overrides", "", "Optional extra overrides TSV")
	fs.Parse(args)

	lem, dur := mustLoad(*dataDir, *overrides)
	lem.Preload()
	st := lem.Stats()
	fmt.Printf("version:   %s (abi=%d)\ndata_dir:  %s\nlemmas:    %d\ntypes:     %d\nadj_forms: %d\nadj_noun:  %d\noverrides: %d\nmorph:     %d\nalt_lem:   %d\nparadigms: %d (%d lemmas)\nload_time: %v\n",
		rulemma.Version, rulemma.ABIVersion, *dataDir, st.Lemmas, st.Types, st.AdjForms, st.AdjNoun, st.Overrides,
		st.Morph, st.AltLemmas, st.Paradigms, st.ParadigmWords, dur.Round(time.Millisecond))
	if m, err := rulemma.LoadManifest(*dataDir); err == nil {
		fmt.Printf("dict_ver:  %s (schema=%d)\n", m.DictVersion, m.SchemaVersion)
	}
}

type cliKeyFlags struct {
	profile          *string
	splitHyphens     *bool
	compactCodes     *bool
	bindPreps        *bool
	bindPurpose      *bool
	keepWords        *string
	excludeAdjToNoun *string
}

func registerKeyFlags(fs *flag.FlagSet) cliKeyFlags {
	return cliKeyFlags{
		profile:          fs.String("profile", "default", "Key profile: default | technical"),
		splitHyphens:     fs.Bool("split-hyphens", false, "Split alphabetic hyphenated words (e.g. онлайн-курсы -> онлайн курсы)"),
		compactCodes:     fs.Bool("compact-codes", false, "Compact model codes (e.g. p-6000 / p 6000 -> p6000)"),
		bindPreps:        fs.Bool("bind-preps", false, "Bind negation prepositions (без/не/кроме/против) to following lemma"),
		bindPurpose:      fs.Bool("bind-purpose", false, "Bind purpose preposition 'для' to following lemma"),
		keepWords:        fs.String("keep-words", "", "Comma-separated canonical lemmas protected from adj->noun folding"),
		excludeAdjToNoun: fs.String("exclude-adj-to-noun", "", "Comma-separated adjective lemmas excluded from adj->noun folding"),
	}
}

func (kf cliKeyFlags) toKeyOptions(stop map[string]struct{}) (rulemma.KeyOptions, bool) {
	prof := strings.TrimSpace(*kf.profile)
	useEx := (prof != "" && !strings.EqualFold(prof, "default")) ||
		*kf.splitHyphens || *kf.compactCodes || *kf.bindPreps || *kf.bindPurpose ||
		strings.TrimSpace(*kf.keepWords) != "" || strings.TrimSpace(*kf.excludeAdjToNoun) != ""
	if !useEx {
		return rulemma.KeyOptions{}, false
	}
	opts := rulemma.KeyOptions{
		Profile:   prof,
		StopWords: stop,
		Tokenizer: rulemma.TokenizerOptions{
			SplitHyphens: *kf.splitHyphens,
			CompactCodes: *kf.compactCodes,
		},
		PreservePrepBindings: *kf.bindPreps,
		BindPurposePrep:      *kf.bindPurpose,
	}
	if kw := strings.TrimSpace(*kf.keepWords); kw != "" {
		opts.KeepWords = rulemma.MakeStopWords(strings.Split(kw, ","))
	}
	if ex := strings.TrimSpace(*kf.excludeAdjToNoun); ex != "" {
		opts.ExcludeAdjToNoun = rulemma.MakeStopWords(strings.Split(ex, ","))
	}
	return opts, true
}

func transformLine(lem *rulemma.Lemmatizer, line, mode string, stop map[string]struct{}, keepYo bool, keyOpts *rulemma.KeyOptions) string {
	if keyOpts != nil {
		switch mode {
		case "key":
			o := *keyOpts
			o.Semantic = false
			return lem.KeyWithOptions(line, o)
		case "semkey":
			o := *keyOpts
			o.Semantic = true
			return lem.KeyWithOptions(line, o)
		case "both":
			o := *keyOpts
			o.Semantic = false
			return lem.LemmatizeString(line) + "\t" + lem.KeyWithOptions(line, o)
		case "all":
			o1 := *keyOpts
			o1.Semantic = false
			o2 := *keyOpts
			o2.Semantic = true
			return lem.LemmatizeString(line) + "\t" + lem.KeyWithOptions(line, o1) + "\t" + lem.KeyWithOptions(line, o2)
		}
	}
	switch mode {
	case "key":
		return lem.LemmaKeyWithStop(line, stop)
	case "semkey":
		return lem.SemanticKeyWithStop(line, stop)
	case "both":
		txt, key, _ := lem.LemmatizeAllWithStop(line, stop)
		return txt + "\t" + key
	case "all":
		txt, key, sem := lem.LemmatizeAllWithStop(line, stop)
		return txt + "\t" + key + "\t" + sem
	case "norm":
		return rulemma.NormalizePhrase(line, keepYo)
	default:
		return lem.LemmatizeString(line)
	}
}

func tsvHeaderSuffix(mode string) string {
	switch mode {
	case "key":
		return "\tlemma_key"
	case "semkey":
		return "\tsemantic_key"
	case "both":
		return "\tlemma_text\tlemma_key"
	case "all":
		return "\tlemma_text\tlemma_key\tsemantic_key"
	case "norm":
		return "\tnorm_phrase"
	default:
		return "\tlemma_text"
	}
}

// processChunks reads lines from r, processes batches in parallel preserving exact order, and writes to w.
func processChunks(r io.Reader, w io.Writer, workers int, fn func(string) string) (int64, error) {
	if workers <= 0 {
		workers = runtime.NumCPU()
	}
	const chunkSize = 4096
	type chunk struct {
		seq   int
		lines []string
	}
	type doneChunk struct {
		seq   int
		lines []string
	}

	jobs := make(chan chunk, workers*2)
	results := make(chan doneChunk, workers*2)

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for c := range jobs {
				out := make([]string, len(c.lines))
				for idx, s := range c.lines {
					out[idx] = fn(s)
				}
				results <- doneChunk{seq: c.seq, lines: out}
			}
		}()
	}

	var writeErr error
	var total int64
	writerDone := make(chan struct{})
	bw := bufio.NewWriterSize(w, 1<<20)
	go func() {
		defer close(writerDone)
		pending := make(map[int][]string)
		nextSeq := 0
		for res := range results {
			pending[res.seq] = res.lines
			for {
				lines, ok := pending[nextSeq]
				if !ok {
					break
				}
				delete(pending, nextSeq)
				for _, line := range lines {
					if _, err := bw.WriteString(line); err != nil && writeErr == nil {
						writeErr = err
					}
					if err := bw.WriteByte('\n'); err != nil && writeErr == nil {
						writeErr = err
					}
					total++
				}
				nextSeq++
			}
		}
		if err := bw.Flush(); err != nil && writeErr == nil {
			writeErr = err
		}
	}()

	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 256*1024), 4*1024*1024)
	seq := 0
	buf := make([]string, 0, chunkSize)
	for sc.Scan() {
		buf = append(buf, sc.Text())
		if len(buf) == chunkSize {
			jobs <- chunk{seq: seq, lines: buf}
			seq++
			buf = make([]string, 0, chunkSize)
		}
	}
	if len(buf) > 0 {
		jobs <- chunk{seq: seq, lines: buf}
	}
	close(jobs)
	wg.Wait()
	close(results)
	<-writerDone

	if err := sc.Err(); err != nil {
		return total, err
	}
	return total, writeErr
}

func cmdLemmatize(args []string) {
	fs := flag.NewFlagSet("lemmatize", flag.ExitOnError)
	dataDir := fs.String("data", defaultDataDir(), "Path to data directory")
	overrides := fs.String("overrides", "", "Optional extra overrides TSV")
	mode := fs.String("mode", "text", "Output mode: text | key | semkey | both | all | norm")
	stopwords := fs.String("stopwords", "", "Custom stopwords: file path, 'none', or comma-separated list (default: built-in)")
	keepYo := fs.Bool("keep-yo", false, "Preserve 'ё' in --mode norm")
	workers := fs.Int("workers", runtime.NumCPU(), "Worker goroutines")
	kf := registerKeyFlags(fs)
	fs.Parse(args)

	stop := mustLoadStopWords(*stopwords)
	var keyOptsPtr *rulemma.KeyOptions
	if ko, ok := kf.toKeyOptions(stop); ok {
		keyOptsPtr = &ko
	}
	var lem *rulemma.Lemmatizer
	if *mode != "norm" {
		lem, _ = mustLoad(*dataDir, *overrides)
	}
	_, err := processChunks(os.Stdin, os.Stdout, *workers, func(s string) string {
		return transformLine(lem, s, *mode, stop, *keepYo, keyOptsPtr)
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func cmdTSV(args []string) {
	fs := flag.NewFlagSet("tsv", flag.ExitOnError)
	inPath := fs.String("in", "", "Input TSV path")
	outPath := fs.String("out", "", "Output TSV path")
	col := fs.Int("col", 0, "0-based column index to lemmatize")
	mode := fs.String("mode", "both", "Appended column(s): text | key | semkey | both | all | norm")
	stopwords := fs.String("stopwords", "", "Custom stopwords: file path, 'none', or comma-separated list (default: built-in)")
	keepYo := fs.Bool("keep-yo", false, "Preserve 'ё' in --mode norm")
	header := fs.Bool("header", false, "Input TSV has a header row")
	dataDir := fs.String("data", defaultDataDir(), "Path to data directory")
	overrides := fs.String("overrides", "", "Optional extra overrides TSV")
	workers := fs.Int("workers", runtime.NumCPU(), "Worker goroutines")
	kf := registerKeyFlags(fs)
	fs.Parse(args)

	if *inPath == "" || *outPath == "" {
		fmt.Fprintln(os.Stderr, "tsv requires --in and --out")
		os.Exit(1)
	}
	stop := mustLoadStopWords(*stopwords)
	var keyOptsPtr *rulemma.KeyOptions
	if ko, ok := kf.toKeyOptions(stop); ok {
		keyOptsPtr = &ko
	}
	var lem *rulemma.Lemmatizer
	var loadDur time.Duration
	if *mode != "norm" {
		lem, loadDur = mustLoad(*dataDir, *overrides)
	}

	inF, err := os.Open(*inPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open input: %v\n", err)
		os.Exit(1)
	}
	defer inF.Close()

	outF, err := os.Create(*outPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "create output: %v\n", err)
		os.Exit(1)
	}
	defer outF.Close()

	br := bufio.NewReaderSize(inF, 1<<20)
	if *header {
		hdr, err := br.ReadString('\n')
		if err != nil && err != io.EOF {
			fmt.Fprintf(os.Stderr, "read header: %v\n", err)
			os.Exit(1)
		}
		hdr = strings.TrimRight(hdr, "\r\n")
		fmt.Fprintln(outF, hdr+tsvHeaderSuffix(*mode))
	}

	t0 := time.Now()
	cIdx := *col
	n, err := processChunks(br, outF, *workers, func(line string) string {
		line = strings.TrimRight(line, "\r")
		parts := strings.Split(line, "\t")
		val := ""
		if cIdx >= 0 && cIdx < len(parts) {
			val = parts[cIdx]
		}
		return line + "\t" + transformLine(lem, val, *mode, stop, *keepYo, keyOptsPtr)
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "process error: %v\n", err)
		os.Exit(1)
	}
	dur := time.Since(t0)
	fmt.Fprintf(os.Stderr, "[rulemma tsv] rows=%d workers=%d load=%v process=%v speed=%.0f rows/s\n",
		n, *workers, loadDur.Round(time.Millisecond), dur.Round(time.Millisecond), float64(n)/dur.Seconds())
}

func cmdBench(args []string) {
	fs := flag.NewFlagSet("bench", flag.ExitOnError)
	inPath := fs.String("in", "", "Input newline-separated queries file")
	dataDir := fs.String("data", defaultDataDir(), "Path to data directory")
	overrides := fs.String("overrides", "", "Optional extra overrides TSV")
	stopwords := fs.String("stopwords", "", "Custom stopwords: file path, 'none', or comma-separated list (default: built-in)")
	workers := fs.Int("workers", runtime.NumCPU(), "Worker goroutines")
	mode := fs.String("mode", "both", "Mode: text | key | semkey | both | all | norm")
	kf := registerKeyFlags(fs)
	fs.Parse(args)

	if *inPath == "" {
		fmt.Fprintln(os.Stderr, "bench requires --in FILE")
		os.Exit(1)
	}
	stop := mustLoadStopWords(*stopwords)
	var keyOptsPtr *rulemma.KeyOptions
	if ko, ok := kf.toKeyOptions(stop); ok {
		keyOptsPtr = &ko
	}
	lem, loadDur := mustLoad(*dataDir, *overrides)
	st := lem.Stats()

	f, err := os.Open(*inPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open: %v\n", err)
		os.Exit(1)
	}
	defer f.Close()

	t0 := time.Now()
	var nLines, nTokens int64
	const chunkSize = 8192
	jobs := make(chan []string, *workers*2)
	var wg sync.WaitGroup
	var mu sync.Mutex
	for i := 0; i < *workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var locLines, locToks int64
			for batch := range jobs {
				for _, line := range batch {
					if keyOptsPtr != nil {
						_ = transformLine(lem, line, *mode, stop, false, keyOptsPtr)
						locToks += int64(len(strings.Fields(line)))
					} else {
						switch *mode {
						case "key":
							_ = lem.LemmaKeyWithStop(line, stop)
							locToks += int64(len(strings.Fields(line)))
						case "semkey":
							_ = lem.SemanticKeyWithStop(line, stop)
							locToks += int64(len(strings.Fields(line)))
						case "text":
							locToks += int64(len(lem.Lemmatize(line)))
						case "all":
							locToks += int64(len(lem.Lemmatize(line)))
							_ = lem.LemmaKeyWithStop(line, stop)
							_ = lem.SemanticKeyWithStop(line, stop)
						case "norm":
							_ = rulemma.NormalizePhrase(line, false)
							locToks += int64(len(strings.Fields(line)))
						default:
							locToks += int64(len(lem.Lemmatize(line)))
							_ = lem.LemmaKeyWithStop(line, stop)
						}
					}
					locLines++
				}
			}
			mu.Lock()
			nLines += locLines
			nTokens += locToks
			mu.Unlock()
		}()
	}

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 512*1024), 4*1024*1024)
	buf := make([]string, 0, chunkSize)
	for sc.Scan() {
		buf = append(buf, sc.Text())
		if len(buf) == chunkSize {
			jobs <- buf
			buf = make([]string, 0, chunkSize)
		}
	}
	if len(buf) > 0 {
		jobs <- buf
	}
	close(jobs)
	wg.Wait()
	dur := time.Since(t0)

	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	fmt.Printf("dict_lemmas:   %d\nload_time:     %v\nworkers:       %d\nmode:          %s\nqueries:       %d\ntokens:        %d\nelapsed:       %v\nqps:           %.0f queries/sec\ntps:           %.0f tokens/sec\nheap_alloc_mb: %.1f MB\n",
		st.Lemmas, loadDur.Round(time.Millisecond), *workers, *mode,
		nLines, nTokens, dur.Round(time.Millisecond),
		float64(nLines)/dur.Seconds(), float64(nTokens)/dur.Seconds(),
		float64(ms.Alloc)/(1024*1024))
}

func cmdSEOName(args []string) {
	fs := flag.NewFlagSet("seoname", flag.ExitOnError)
	inPath := fs.String("in", "", "Input CSV (id_pdp,sku_name,type_name,keywords)")
	outPath := fs.String("out", "", "Output CSV")
	maxLen := fs.Int("max-len", 100, "Max SEO name length in runes")
	dataDir := fs.String("data", defaultDataDir(), "Path to data directory")
	overrides := fs.String("overrides", "", "Optional extra overrides TSV")
	workers := fs.Int("workers", runtime.NumCPU(), "Worker goroutines")
	fs.Parse(args)

	if *inPath == "" || *outPath == "" {
		fmt.Fprintln(os.Stderr, "seoname requires --in and --out")
		os.Exit(1)
	}
	lem, _ := mustLoad(*dataDir, *overrides)

	f, err := os.Open(*inPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open: %v\n", err)
		os.Exit(1)
	}
	defer f.Close()
	r := csv.NewReader(f)
	hdr, err := r.Read()
	if err != nil {
		fmt.Fprintf(os.Stderr, "read header: %v\n", err)
		os.Exit(1)
	}
	cols := map[string]int{}
	for i, h := range hdr {
		cols[strings.TrimSpace(h)] = i
	}
	idC, ok1 := cols["id_pdp"]
	skuC, ok2 := cols["sku_name"]
	typeC, ok3 := cols["type_name"]
	kwC, ok4 := cols["keywords"]
	if !ok1 || !ok2 || !ok3 || !ok4 {
		fmt.Fprintf(os.Stderr, "missing required columns (id_pdp, sku_name, type_name, keywords)\n")
		os.Exit(1)
	}

	type rowIn struct {
		idx int
		rec []string
	}
	var all []rowIn
	for idx := 0; ; idx++ {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "read row: %v\n", err)
			os.Exit(1)
		}
		all = append(all, rowIn{idx: idx, rec: rec})
	}

	out := make([][]string, len(all))
	jobs := make(chan rowIn, *workers*4)
	var wg sync.WaitGroup
	for i := 0; i < *workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for item := range jobs {
				kws := strings.Split(item.rec[kwC], "|")
				seo, added, agreed := lem.BuildSEOName(item.rec[skuC], item.rec[typeC], kws, *maxLen)
				out[item.idx] = []string{
					item.rec[idC], item.rec[skuC], item.rec[typeC], seo,
					fmt.Sprintf("%d", added), fmt.Sprintf("%d", agreed), fmt.Sprintf("%d", len([]rune(seo))),
				}
			}
		}()
	}
	for _, it := range all {
		jobs <- it
	}
	close(jobs)
	wg.Wait()

	of, err := os.Create(*outPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "create output: %v\n", err)
		os.Exit(1)
	}
	defer of.Close()
	w := csv.NewWriter(of)
	w.Write([]string{"id_pdp", "sku_name", "type_name", "seo_name", "num_added", "num_agreed", "seo_len"})
	for _, rec := range out {
		w.Write(rec)
	}
	w.Flush()
	if err := w.Error(); err != nil {
		fmt.Fprintf(os.Stderr, "write error: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "[rulemma seoname] processed %d products\n", len(out))
}

func cmdAnalyze(args []string) {
	fs := flag.NewFlagSet("analyze", flag.ExitOnError)
	dataDir := fs.String("data", defaultDataDir(), "Path to data directory")
	overrides := fs.String("overrides", "", "Optional extra overrides TSV")
	fs.Parse(args)

	lem, _ := mustLoad(*dataDir, *overrides)
	printWord := func(w string) {
		info := lem.Analyze(w)
		b, _ := json.Marshal(info)
		fmt.Println(string(b))
	}
	if fs.NArg() > 0 {
		for _, w := range fs.Args() {
			printWord(w)
		}
		return
	}
	sc := bufio.NewScanner(os.Stdin)
	for sc.Scan() {
		w := strings.TrimSpace(sc.Text())
		if w != "" {
			printWord(w)
		}
	}
}

func cmdInflect(args []string) {
	fs := flag.NewFlagSet("inflect", flag.ExitOnError)
	dataDir := fs.String("data", defaultDataDir(), "Path to data directory")
	overrides := fs.String("overrides", "", "Optional extra overrides TSV")
	targetCase := fs.String("case", "gent", "Target case: nomn | gent | datv | accs | ablt | loct")
	targetNum := fs.String("number", "", "Optional target number: sing | plur")
	fs.Parse(args)

	lem, _ := mustLoad(*dataDir, *overrides)
	processOne := func(s string) {
		res, _ := lem.InflectPhrase(s, *targetCase, *targetNum)
		fmt.Println(res)
	}
	if fs.NArg() > 0 {
		processOne(strings.Join(fs.Args(), " "))
		return
	}
	sc := bufio.NewScanner(os.Stdin)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			fmt.Println("")
			continue
		}
		processOne(line)
	}
}
