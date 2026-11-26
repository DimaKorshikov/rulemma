// Command rulemma is a fast, concurrent Russian & e-commerce lemmatizer CLI.
//
// Subcommands:
//
//	rulemma info      [--data data]
//	rulemma lemmatize [--data data] [--mode text|key|both] [--workers N]
//	rulemma tsv       --in IN.tsv --out OUT.tsv [--col 0] [--mode key|text|both] [--header] [--data data] [--workers N]
//	rulemma bench     --in QUERIES.txt [--data data] [--workers N]
//	rulemma seoname   --in PRODUCTS.csv --out RESULT.csv [--data data] [--workers N]
package main

import (
	"bufio"
	"encoding/csv"
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
  rulemma info      [--data DIR] [--overrides FILE]
  rulemma lemmatize [--data DIR] [--overrides FILE] [--mode text|key|both] [--workers N]
  rulemma tsv       --in IN.tsv --out OUT.tsv [--col 0] [--mode key|text|both] [--header] [--data DIR] [--workers N]
  rulemma bench     --in FILE [--data DIR] [--workers N]
  rulemma seoname   --in PRODUCTS.csv --out RESULT.csv [--data DIR] [--workers N]`)
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

func cmdInfo(args []string) {
	fs := flag.NewFlagSet("info", flag.ExitOnError)
	dataDir := fs.String("data", defaultDataDir(), "Path to data directory")
	overrides := fs.String("overrides", "", "Optional extra overrides TSV")
	fs.Parse(args)

	lem, dur := mustLoad(*dataDir, *overrides)
	st := lem.Stats()
	fmt.Printf("data_dir:  %s\nlemmas:    %d\ntypes:     %d\nadj_forms: %d\nadj_noun:  %d\noverrides: %d\nload_time: %v\n",
		*dataDir, st.Lemmas, st.Types, st.AdjForms, st.AdjNoun, st.Overrides, dur.Round(time.Millisecond))
}

func transformLine(lem *rulemma.Lemmatizer, line, mode string) string {
	switch mode {
	case "key":
		return lem.LemmaKey(line)
	case "semkey":
		return lem.SemanticKey(line)
	case "both":
		return lem.LemmatizeString(line) + "\t" + lem.LemmaKey(line)
	case "all":
		return lem.LemmatizeString(line) + "\t" + lem.LemmaKey(line) + "\t" + lem.SemanticKey(line)
	default:
		return lem.LemmatizeString(line)
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
	mode := fs.String("mode", "text", "Output mode: text | key | both")
	workers := fs.Int("workers", runtime.NumCPU(), "Worker goroutines")
	fs.Parse(args)

	lem, _ := mustLoad(*dataDir, *overrides)
	_, err := processChunks(os.Stdin, os.Stdout, *workers, func(s string) string {
		return transformLine(lem, s, *mode)
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
	mode := fs.String("mode", "both", "Appended column(s): text | key | both")
	header := fs.Bool("header", false, "Input TSV has a header row")
	dataDir := fs.String("data", defaultDataDir(), "Path to data directory")
	overrides := fs.String("overrides", "", "Optional extra overrides TSV")
	workers := fs.Int("workers", runtime.NumCPU(), "Worker goroutines")
	fs.Parse(args)

	if *inPath == "" || *outPath == "" {
		fmt.Fprintln(os.Stderr, "tsv requires --in and --out")
		os.Exit(1)
	}
	lem, loadDur := mustLoad(*dataDir, *overrides)

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
		suffix := "\tlemma_text"
		if *mode == "key" {
			suffix = "\tlemma_key"
		} else if *mode == "both" {
			suffix = "\tlemma_text\tlemma_key"
		}
		fmt.Fprintln(outF, hdr+suffix)
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
		return line + "\t" + transformLine(lem, val, *mode)
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
	workers := fs.Int("workers", runtime.NumCPU(), "Worker goroutines")
	mode := fs.String("mode", "both", "Mode: text | key | both")
	fs.Parse(args)

	if *inPath == "" {
		fmt.Fprintln(os.Stderr, "bench requires --in FILE")
		os.Exit(1)
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
					if *mode == "key" {
						_ = lem.LemmaKey(line)
						locToks += int64(len(strings.Fields(line)))
					} else if *mode == "text" {
						locToks += int64(len(lem.Lemmatize(line)))
					} else {
						locToks += int64(len(lem.Lemmatize(line)))
						_ = lem.LemmaKey(line)
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
	type rowOut struct {
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
