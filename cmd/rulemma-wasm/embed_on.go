//go:build js && wasm && wasm_embed

package main

import (
	"github.com/DimaKorshikov/rulemma"
	"github.com/DimaKorshikov/rulemma/embedded"
)

func tryLoadEmbedded() (*rulemma.Lemmatizer, error) {
	return embedded.Load()
}
