// Package main builds a C-shared library (`librulemma.so` / `librulemma.dylib`) for zero-overhead
// in-process Python (ctypes/cffi), Rust, C/C++, or Node.js bindings.
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

//export rulemma_word
func rulemma_word(h C.int64_t, word *C.char) *C.char {
	lem := getLem(int64(h))
	if lem == nil {
		return nil
	}
	return C.CString(lem.Word(C.GoString(word)))
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

//export rulemma_semantic_key
func rulemma_semantic_key(h C.int64_t, text *C.char) *C.char {
	lem := getLem(int64(h))
	if lem == nil {
		return nil
	}
	return C.CString(lem.SemanticKey(C.GoString(text)))
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

// rulemma_batch processes newline-separated UTF-8 texts across parallel Go goroutines (bypassing Python GIL!).
// mode: 0 = lemmatize text, 1 = lemma_key, 2 = semantic_key, 3 = text\tkey, 4 = text\tkey\tsemkey
//
//export rulemma_batch
func rulemma_batch(h C.int64_t, payload *C.char, payloadLen C.int, mode C.int, workers C.int, outLen *C.int) *C.char {
	lem := getLem(int64(h))
	if lem == nil {
		return nil
	}
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
			return lem.LemmaKey(s)
		case 2:
			return lem.SemanticKey(s)
		case 3:
			return lem.LemmatizeString(s) + "\t" + lem.LemmaKey(s)
		case 4:
			return lem.LemmatizeString(s) + "\t" + lem.LemmaKey(s) + "\t" + lem.SemanticKey(s)
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

func main() {}
