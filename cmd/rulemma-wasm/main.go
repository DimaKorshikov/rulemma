//go:build js && wasm

// Reference WebAssembly entry point for rulemma.
//
// Supports three build profiles with zero code changes:
//  1. Thin WASM (default, ~0.7–0.9 MB gzip): no embedded dictionaries; browser fetches
//     only the needed data/*.tsv.gz files and initializes via rulemmaInit / rulemmaLoadTable.
//  2. Core embedded (`-tags wasm_embed,rulemma_core`, ~1.9 MB gzip): embeds core tables (~1.2 MB).
//  3. Full embedded (`-tags wasm_embed`, ~3.3 MB gzip): embeds all 8 tables (~2.6 MB).
package main

import (
	"encoding/json"
	"strings"
	"syscall/js"

	"github.com/DimaKorshikov/rulemma"
)

var lem *rulemma.Lemmatizer

func jsonResp(ok bool, data any, errMsg string) string {
	type resp struct {
		OK    bool   `json:"ok"`
		Data  any    `json:"data,omitempty"`
		Error string `json:"error,omitempty"`
	}
	b, _ := json.Marshal(resp{OK: ok, Data: data, Error: errMsg})
	return string(b)
}

func jsBytes(v js.Value) []byte {
	n := v.Get("byteLength").Int()
	buf := make([]byte, n)
	js.CopyBytesToGo(buf, v)
	return buf
}

func main() {
	if embeddedLem, err := tryLoadEmbedded(); err == nil && embeddedLem != nil {
		lem = embeddedLem
	}

	js.Global().Set("rulemmaVersion", js.FuncOf(func(this js.Value, args []js.Value) any {
		return rulemma.Version
	}))

	// rulemmaInit({ "lemmas.tsv.gz": Uint8Array, "overrides.tsv": Uint8Array, ... })
	js.Global().Set("rulemmaInit", js.FuncOf(func(this js.Value, args []js.Value) any {
		if len(args) < 1 || args[0].Type() != js.TypeObject {
			return jsonResp(false, nil, "expected object mapping filename -> Uint8Array")
		}
		keys := js.Global().Get("Object").Call("keys", args[0])
		files := make(map[string][]byte, keys.Length())
		for i := 0; i < keys.Length(); i++ {
			k := keys.Index(i).String()
			files[k] = jsBytes(args[0].Get(k))
		}
		l, err := rulemma.LoadMemory(files, rulemma.Options{})
		if err != nil {
			return jsonResp(false, nil, err.Error())
		}
		lem = l
		return jsonResp(true, l.Stats(), "")
	}))

	// rulemmaLoadTable("paradigms.tsv.gz", Uint8Array)
	js.Global().Set("rulemmaLoadTable", js.FuncOf(func(this js.Value, args []js.Value) any {
		if lem == nil {
			return jsonResp(false, nil, "not initialized")
		}
		if len(args) < 2 {
			return jsonResp(false, nil, "expected (name, Uint8Array)")
		}
		if err := lem.LoadTable(args[0].String(), jsBytes(args[1])); err != nil {
			return jsonResp(false, nil, err.Error())
		}
		return jsonResp(true, lem.Stats(), "")
	}))

	js.Global().Set("rulemmaCall", js.FuncOf(func(this js.Value, args []js.Value) any {
		if lem == nil {
			return jsonResp(false, nil, "not initialized")
		}
		if len(args) < 2 {
			return jsonResp(false, nil, "expected (method, payloadJSON)")
		}
		method := args[0].String()
		payload := args[1].String()
		switch method {
		case "stats":
			return jsonResp(true, lem.Stats(), "")
		case "word":
			w, src := lem.LookupWord(payload)
			return jsonResp(true, map[string]string{"lemma": w, "source": src}, "")
		case "lemmatize":
			return jsonResp(true, lem.LemmatizeString(payload), "")
		case "lemma_key":
			return jsonResp(true, lem.LemmaKey(payload), "")
		case "semantic_key":
			return jsonResp(true, lem.SemanticKey(payload), "")
		case "analyze":
			return jsonResp(true, lem.Analyze(payload), "")
		case "inflect_phrase":
			parts := strings.SplitN(payload, "\t", 3)
			if len(parts) < 2 {
				return jsonResp(false, nil, "expected phrase\\tcase[\\tnumber]")
			}
			num := ""
			if len(parts) == 3 {
				num = parts[2]
			}
			res, ok := lem.InflectPhrase(parts[0], parts[1], num)
			return jsonResp(true, map[string]any{"result": res, "changed": ok}, "")
		default:
			return jsonResp(false, nil, "unknown method: "+method)
		}
	}))

	select {}
}
