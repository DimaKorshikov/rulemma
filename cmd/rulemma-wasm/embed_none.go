//go:build js && wasm && !wasm_embed

package main

import "github.com/DimaKorshikov/rulemma"

func tryLoadEmbedded() (*rulemma.Lemmatizer, error) {
	return nil, nil
}
