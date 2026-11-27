// Package main builds a C-shared library (`librulemma.so` / `librulemma.dylib`) for zero-overhead
// in-process Python (ctypes/cffi), Rust, C/C++, or Node.js bindings.
//
// C-ABI Contract: strictly append-only within the same ABIVersion. Existing exported symbols
// and mode codes (0..4) never change signatures or semantics.
//
// Build:
//
//	go build -buildmode=c-shared -o python/rulemma/librulemma.so ./capi
package main

/*
#include <stdlib.h>
#include <stdint.h>
*/
import "C"

import (
	"encoding/json"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/DimaKorshikov/rulemma"
)

var (
	handlesMu sync.RWMutex
	handles   = map[int64]*rulemma.Lemmatizer{}
	nextID    int64
	lastErrMu sync.Mutex
	lastErr   string
)

func setErr(err error) {
	lastErrMu.Lock()
	if err != nil {
		lastErr = err.Error()
	} else {
		lastErr = ""
	}
	lastErrMu.Unlock()
}

func getLem(h int64) *rulemma.Lemmatizer {
	handlesMu.RLock()
	lem := handles[h]
	handlesMu.RUnlock()
	return lem
}

func parseStopWordsC(stopwordsNewline *C.char) map[string]struct{} {
	if stopwordsNewline == nil {
		return rulemma.DefaultKeyStopWords
	}
	raw := C.GoString(stopwordsNewline)
	if raw == "" {
		return map[string]struct{}{}
	}
	return rulemma.MakeStopWords(strings.Split(raw, "\n"))
}

//export rulemma_abi_version
func rulemma_abi_version() C.int {
	return C.int(rulemma.ABIVersion)
}

//export rulemma_version
func rulemma_version() *C.char {
	return C.CString(rulemma.Version)
}

//export rulemma_last_error
func rulemma_last_error() *C.char {
	lastErrMu.Lock()
	s := lastErr
	lastErrMu.Unlock()
	return C.CString(s)
}

//export rulemma_free_str
func rulemma_free_str(p *C.char) {
	if p != nil {
		C.free(unsafe.Pointer(p))
	}
}

//export rulemma_load
func rulemma_load(dataDir *C.char, overridesPath *C.char) C.int64_t {
	dir := C.GoString(dataDir)
	ov := ""
	if overridesPath != nil {
		ov = C.GoString(overridesPath)
	}
	lem, err := rulemma.LoadWithOptions(dir, rulemma.Options{ExtraOverridesPath: ov})
	if err != nil {
		setErr(err)
		return 0
	}
	setErr(nil)
	id := atomic.AddInt64(&nextID, 1)
	handlesMu.Lock()
	handles[id] = lem
	handlesMu.Unlock()
	return C.int64_t(id)
}

//export rulemma_free
func rulemma_free(h C.int64_t) {
	handlesMu.Lock()
	delete(handles, int64(h))
	handlesMu.Unlock()
}

//export rulemma_stats
func rulemma_stats(h C.int64_t, outLemmas, outTypes, outAdjForms, outAdjNoun, outOverrides *C.int64_t) C.int {
	lem := getLem(int64(h))
	if lem == nil {
		return 0
	}
	st := lem.Stats()
	if outLemmas != nil {
		*outLemmas = C.int64_t(st.Lemmas)
	}
	if outTypes != nil {
		*outTypes = C.int64_t(st.Types)
	}
	if outAdjForms != nil {
		*outAdjForms = C.int64_t(st.AdjForms)
	}
	if outAdjNoun != nil {
		*outAdjNoun = C.int64_t(st.AdjNoun)
	}
	if outOverrides != nil {
		*outOverrides = C.int64_t(st.Overrides)
	}
	return 1
}

//export rulemma_normalize_phrase
func rulemma_normalize_phrase(text *C.char, keepYo C.int) *C.char {
	if text == nil {
		return C.CString("")
	}
	return C.CString(rulemma.NormalizePhrase(C.GoString(text), keepYo != 0))
}

//export rulemma_word
func rulemma_word(h C.int64_t, word *C.char) *C.char {
	lem := getLem(int64(h))
	if lem == nil {
		return nil
	}
	return C.CString(lem.Word(C.GoString(word)))
}

// rulemma_lookup_word returns the lemma and writes provenance code to outSource:
// 0 = oov, 1 = dict, 2 = override, 3 = rule.
//
//export rulemma_lookup_word
func rulemma_lookup_word(h C.int64_t, word *C.char, outSource *C.int) *C.char {
	lem := getLem(int64(h))
	if lem == nil {
		return nil
	}
	res, src := lem.LookupWord(C.GoString(word))
	if outSource != nil {
		switch src {
		case rulemma.SourceDict:
			*outSource = 1
		case rulemma.SourceOverride:
			*outSource = 2
		case rulemma.SourceRule:
			*outSource = 3
		default:
			*outSource = 0
		}
	}
	return C.CString(res)
}

//export rulemma_dict_version
func rulemma_dict_version(h C.int64_t) *C.char {
	lem := getLem(int64(h))
	if lem == nil {
		return nil
	}
	if m := lem.Manifest(); m != nil {
		return C.CString(m.DictVersion)
	}
	return C.CString("")
}

//export rulemma_lemmatize
func rulemma_lemmatize(h C.int64_t, text *C.char) *C.char {
	lem := getLem(int64(h))
	if lem == nil {
		return nil
	}
	return C.CString(lem.LemmatizeString(C.GoString(text)))
}

//export rulemma_lemma_key
func rulemma_lemma_key(h C.int64_t, text *C.char) *C.char {
	lem := getLem(int64(h))
	if lem == nil {
		return nil
	}
	return C.CString(lem.LemmaKey(C.GoString(text)))
}

//export rulemma_lemma_key_stop
func rulemma_lemma_key_stop(h C.int64_t, text *C.char, stopwordsNewline *C.char) *C.char {
	lem := getLem(int64(h))
	if lem == nil {
		return nil
	}
	stop := parseStopWordsC(stopwordsNewline)
	return C.CString(lem.LemmaKeyWithStop(C.GoString(text), stop))
}

//export rulemma_semantic_key
func rulemma_semantic_key(h C.int64_t, text *C.char) *C.char {
	lem := getLem(int64(h))
	if lem == nil {
		return nil
	}
	return C.CString(lem.SemanticKey(C.GoString(text)))
}

//export rulemma_semantic_key_stop
func rulemma_semantic_key_stop(h C.int64_t, text *C.char, stopwordsNewline *C.char) *C.char {
	lem := getLem(int64(h))
	if lem == nil {
		return nil
	}
	stop := parseStopWordsC(stopwordsNewline)
	return C.CString(lem.SemanticKeyWithStop(C.GoString(text), stop))
}

//export rulemma_adj_to_noun
func rulemma_adj_to_noun(h C.int64_t, word *C.char, outMatched *C.int) *C.char {
	lem := getLem(int64(h))
	if lem == nil {
		return nil
	}
	res, ok := lem.AdjToNoun(C.GoString(word))
	if outMatched != nil {
		if ok {
			*outMatched = 1
		} else {
			*outMatched = 0
		}
	}
	return C.CString(res)
}

//export rulemma_agree_adjective
func rulemma_agree_adjective(h C.int64_t, adj *C.char, productType *C.char, outChanged *C.int) *C.char {
	lem := getLem(int64(h))
	if lem == nil {
		return nil
	}
	res, changed := lem.AgreeAdjective(C.GoString(adj), C.GoString(productType))
	if outChanged != nil {
		if changed {
			*outChanged = 1
		} else {
			*outChanged = 0
		}
	}
	return C.CString(res)
}

//export rulemma_build_seo_name
func rulemma_build_seo_name(h C.int64_t, baseName *C.char, productType *C.char, keywordsPipe *C.char, maxLen C.int, outAdded *C.int, outAgreed *C.int) *C.char {
	lem := getLem(int64(h))
	if lem == nil {
		return nil
	}
	var kws []string
	if keywordsPipe != nil {
		raw := C.GoString(keywordsPipe)
		if raw != "" {
			kws = strings.Split(raw, "\n")
		}
	}
	seo, added, agreed := lem.BuildSEOName(C.GoString(baseName), C.GoString(productType), kws, int(maxLen))
	if outAdded != nil {
		*outAdded = C.int(added)
	}
	if outAgreed != nil {
		*outAgreed = C.int(agreed)
	}
	return C.CString(seo)
}

func runBatch(lem *rulemma.Lemmatizer, payload *C.char, payloadLen C.int, mode C.int, stop map[string]struct{}, workers C.int, outLen *C.int) *C.char {
	if payloadLen <= 0 || payload == nil {
		if outLen != nil {
			*outLen = 0
		}
		return C.CString("")
	}

	raw := C.GoStringN(payload, payloadLen)
	lines := strings.Split(raw, "\n")
	n := len(lines)
	out := make([]string, n)

	w := int(workers)
	if w <= 0 {
		w = runtime.NumCPU()
	}
	if n < 512 {
		w = 1
	} else if w > n {
		w = n
	}

	m := int(mode)
	apply := func(s string) string {
		switch m {
		case 1:
			return lem.LemmaKeyWithStop(s, stop)
		case 2:
			return lem.SemanticKeyWithStop(s, stop)
		case 3:
			txt, key, _ := lem.LemmatizeAllWithStop(s, stop)
			return txt + "\t" + key
		case 4:
			txt, key, sem := lem.LemmatizeAllWithStop(s, stop)
			return txt + "\t" + key + "\t" + sem
		case 5:
			return rulemma.NormalizePhrase(s, false)
		case 6:
			return rulemma.NormalizePhrase(s, true)
		default:
			return lem.LemmatizeString(s)
		}
	}

	if w == 1 {
		for i, s := range lines {
			out[i] = apply(s)
		}
	} else {
		chunk := (n + w - 1) / w
		var wg sync.WaitGroup
		for wi := 0; wi < w; wi++ {
			start := wi * chunk
			if start >= n {
				break
			}
			end := start + chunk
			if end > n {
				end = n
			}
			wg.Add(1)
			go func(a, b int) {
				defer wg.Done()
				for i := a; i < b; i++ {
					out[i] = apply(lines[i])
				}
			}(start, end)
		}
		wg.Wait()
	}

	joined := strings.Join(out, "\n")
	if outLen != nil {
		*outLen = C.int(len(joined))
	}
	return C.CString(joined)
}

// rulemma_batch processes newline-separated UTF-8 texts across parallel Go goroutines (bypassing Python GIL!).
// mode: 0 = lemmatize text, 1 = lemma_key, 2 = semantic_key, 3 = text\tkey, 4 = text\tkey\tsemkey,
// 5 = normalize_phrase (ё->е), 6 = normalize_phrase (keep ё).
//
//export rulemma_batch
func rulemma_batch(h C.int64_t, payload *C.char, payloadLen C.int, mode C.int, workers C.int, outLen *C.int) *C.char {
	lem := getLem(int64(h))
	if lem == nil {
		return nil
	}
	return runBatch(lem, payload, payloadLen, mode, rulemma.DefaultKeyStopWords, workers, outLen)
}

// rulemma_batch_stop is the custom stop-words variant of rulemma_batch.
// stopwordsNewline: NULL uses DefaultKeyStopWords, "" uses an empty stop set,
// otherwise newline-separated stop words.
//
//export rulemma_batch_stop
func rulemma_batch_stop(h C.int64_t, payload *C.char, payloadLen C.int, mode C.int, stopwordsNewline *C.char, workers C.int, outLen *C.int) *C.char {
	lem := getLem(int64(h))
	if lem == nil {
		return nil
	}
	stop := parseStopWordsC(stopwordsNewline)
	return runBatch(lem, payload, payloadLen, mode, stop, workers, outLen)
}

type cKeyOptionsJSON struct {
	Semantic             bool     `json:"semantic"`
	Profile              string   `json:"profile"`
	StopWords            []string `json:"stopwords"`
	HasStopWords         bool     `json:"has_stopwords"`
	NormalizeHomoglyphs  bool     `json:"normalize_homoglyphs"`
	SplitHyphens         bool     `json:"split_hyphens"`
	SplitNumberUnit      bool     `json:"split_number_unit"`
	CompactCodes         bool     `json:"compact_codes"`
	PreservePrepBindings bool     `json:"preserve_prep_bindings"`
	BindPurposePrep      bool     `json:"bind_purpose_prep"`
	KeepWords            []string `json:"keep_words"`
	ExcludeAdjToNoun     []string `json:"exclude_adj_to_noun"`
}

func decodeKeyOptionsC(optsJSON *C.char) rulemma.KeyOptions {
	if optsJSON == nil {
		return rulemma.KeyOptions{}
	}
	raw := C.GoString(optsJSON)
	if raw == "" {
		return rulemma.KeyOptions{}
	}
	var cfg cKeyOptionsJSON
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return rulemma.KeyOptions{}
	}
	opts := rulemma.KeyOptions{
		Semantic: cfg.Semantic,
		Profile:  cfg.Profile,
		Tokenizer: rulemma.TokenizerOptions{
			NormalizeHomoglyphs: cfg.NormalizeHomoglyphs,
			SplitHyphens:        cfg.SplitHyphens,
			SplitNumberUnit:     cfg.SplitNumberUnit,
			CompactCodes:        cfg.CompactCodes,
		},
		PreservePrepBindings: cfg.PreservePrepBindings,
		BindPurposePrep:      cfg.BindPurposePrep,
	}
	if cfg.HasStopWords {
		opts.StopWords = rulemma.MakeStopWords(cfg.StopWords)
	}
	if len(cfg.KeepWords) > 0 {
		opts.KeepWords = rulemma.MakeStopWords(cfg.KeepWords)
	}
	if len(cfg.ExcludeAdjToNoun) > 0 {
		opts.ExcludeAdjToNoun = rulemma.MakeStopWords(cfg.ExcludeAdjToNoun)
	}
	return opts
}

//export rulemma_normalize_homoglyphs
func rulemma_normalize_homoglyphs(text *C.char) *C.char {
	if text == nil {
		return C.CString("")
	}
	return C.CString(rulemma.NormalizeHomoglyphs(C.GoString(text)))
}

//export rulemma_tokenize_tech
func rulemma_tokenize_tech(text *C.char, optsJSON *C.char) *C.char {
	if text == nil {
		return C.CString("[]")
	}
	var topts rulemma.TokenizerOptions
	if optsJSON != nil {
		raw := C.GoString(optsJSON)
		if raw != "" {
			var cfg cKeyOptionsJSON
			if json.Unmarshal([]byte(raw), &cfg) == nil {
				topts = rulemma.TokenizerOptions{
					NormalizeHomoglyphs: cfg.NormalizeHomoglyphs,
					SplitHyphens:        cfg.SplitHyphens,
					SplitNumberUnit:     cfg.SplitNumberUnit,
					CompactCodes:        cfg.CompactCodes,
				}
			}
		}
	}
	toks := rulemma.TokenizeTechnical(C.GoString(text), topts)
	type tokOut struct {
		Text   string `json:"text"`
		IsTech bool   `json:"is_tech"`
		IsNum  bool   `json:"is_num"`
	}
	res := make([]tokOut, len(toks))
	for i, tk := range toks {
		res[i] = tokOut{Text: tk.Text, IsTech: tk.IsTech, IsNum: tk.IsNum}
	}
	b, _ := json.Marshal(res)
	return C.CString(string(b))
}

//export rulemma_key_ex
func rulemma_key_ex(h C.int64_t, text *C.char, optsJSON *C.char) *C.char {
	lem := getLem(int64(h))
	if lem == nil {
		return nil
	}
	opts := decodeKeyOptionsC(optsJSON)
	return C.CString(lem.KeyWithOptions(C.GoString(text), opts))
}

//export rulemma_batch_ex
func rulemma_batch_ex(h C.int64_t, payload *C.char, payloadLen C.int, optsJSON *C.char, workers C.int, outLen *C.int) *C.char {
	lem := getLem(int64(h))
	if lem == nil {
		return nil
	}
	opts := decodeKeyOptionsC(optsJSON)
	return runBatchFn(payload, payloadLen, workers, outLen, func(s string) string {
		return lem.KeyWithOptions(s, opts)
	})
}

// rulemma_batch_all_ex returns "lemmatized_text\tlemma_key\tsemantic_key" per line using KeyOptions JSON
// (one-pass lemmatize_all with the v0.3 technical profile and key options).
//
//export rulemma_batch_all_ex
func rulemma_batch_all_ex(h C.int64_t, payload *C.char, payloadLen C.int, optsJSON *C.char, workers C.int, outLen *C.int) *C.char {
	lem := getLem(int64(h))
	if lem == nil {
		return nil
	}
	opts := decodeKeyOptionsC(optsJSON)
	return runBatchFn(payload, payloadLen, workers, outLen, func(s string) string {
		txt, key, sem := lem.LemmatizeAllWithOptions(s, opts)
		return txt + "\t" + key + "\t" + sem
	})
}

func runBatchFn(payload *C.char, payloadLen C.int, workers C.int, outLen *C.int, apply func(string) string) *C.char {
	if payloadLen <= 0 || payload == nil {
		if outLen != nil {
			*outLen = 0
		}
		return C.CString("")
	}
	raw := C.GoStringN(payload, payloadLen)
	lines := strings.Split(raw, "\n")
	n := len(lines)
	out := make([]string, n)

	w := int(workers)
	if w <= 0 {
		w = runtime.NumCPU()
	}
	if n < 512 {
		w = 1
	} else if w > n {
		w = n
	}

	if w == 1 {
		for i, s := range lines {
			out[i] = apply(s)
		}
	} else {
		chunk := (n + w - 1) / w
		var wg sync.WaitGroup
		for wi := 0; wi < w; wi++ {
			start := wi * chunk
			if start >= n {
				break
			}
			end := start + chunk
			if end > n {
				end = n
			}
			wg.Add(1)
			go func(a, b int) {
				defer wg.Done()
				for i := a; i < b; i++ {
					out[i] = apply(lines[i])
				}
			}(start, end)
		}
		wg.Wait()
	}

	joined := strings.Join(out, "\n")
	if outLen != nil {
		*outLen = C.int(len(joined))
	}
	return C.CString(joined)
}

//export rulemma_analyze
func rulemma_analyze(h C.int64_t, word *C.char) *C.char {
	lem := getLem(int64(h))
	if lem == nil {
		return nil
	}
	info := lem.Analyze(C.GoString(word))
	b, _ := json.Marshal(info)
	return C.CString(string(b))
}

//export rulemma_prep_governs
func rulemma_prep_governs(h C.int64_t, prep *C.char, nextWord *C.char) C.int {
	lem := getLem(int64(h))
	if lem == nil {
		return 0
	}
	if lem.PrepGoverns(C.GoString(prep), C.GoString(nextWord)) {
		return 1
	}
	return 0
}

//export rulemma_inflect
func rulemma_inflect(h C.int64_t, word *C.char, targetCase *C.char, targetNumber *C.char, outOK *C.int) *C.char {
	lem := getLem(int64(h))
	if lem == nil {
		return nil
	}
	num := ""
	if targetNumber != nil {
		num = C.GoString(targetNumber)
	}
	res, ok := lem.Inflect(C.GoString(word), C.GoString(targetCase), num)
	if outOK != nil {
		if ok {
			*outOK = 1
		} else {
			*outOK = 0
		}
	}
	return C.CString(res)
}

//export rulemma_inflect_adj
func rulemma_inflect_adj(h C.int64_t, adj *C.char, targetCase *C.char, gender *C.char, number *C.char, animate C.int, outOK *C.int) *C.char {
	lem := getLem(int64(h))
	if lem == nil {
		return nil
	}
	res, ok := lem.InflectAdj(C.GoString(adj), C.GoString(targetCase), C.GoString(gender), C.GoString(number), animate != 0)
	if outOK != nil {
		if ok {
			*outOK = 1
		} else {
			*outOK = 0
		}
	}
	return C.CString(res)
}

//export rulemma_inflect_phrase
func rulemma_inflect_phrase(h C.int64_t, phrase *C.char, targetCase *C.char, targetNumber *C.char, outOK *C.int) *C.char {
	lem := getLem(int64(h))
	if lem == nil {
		return nil
	}
	num := ""
	if targetNumber != nil {
		num = C.GoString(targetNumber)
	}
	res, ok := lem.InflectPhrase(C.GoString(phrase), C.GoString(targetCase), num)
	if outOK != nil {
		if ok {
			*outOK = 1
		} else {
			*outOK = 0
		}
	}
	return C.CString(res)
}

//export rulemma_all_forms
func rulemma_all_forms(h C.int64_t, word *C.char) *C.char {
	lem := getLem(int64(h))
	if lem == nil {
		return nil
	}
	forms := lem.AllForms(C.GoString(word))
	if forms == nil {
		forms = []string{}
	}
	b, _ := json.Marshal(forms)
	return C.CString(string(b))
}

//export rulemma_agree_number
func rulemma_agree_number(h C.int64_t, n C.double, word *C.char) *C.char {
	lem := getLem(int64(h))
	if lem == nil {
		return nil
	}
	return C.CString(lem.AgreeNumber(float64(n), C.GoString(word)))
}

//export rulemma_preposition
func rulemma_preposition(prep *C.char, nextWord *C.char) *C.char {
	if prep == nil {
		return C.CString("")
	}
	nw := ""
	if nextWord != nil {
		nw = C.GoString(nextWord)
	}
	return C.CString(rulemma.Preposition(C.GoString(prep), nw))
}

func main() {}
