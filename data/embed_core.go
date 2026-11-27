//go:build rulemma_core

// Package data exports the lightweight core embedded dictionaries (~1.2 MB)
// when compiled with `-tags rulemma_core` (omits adj_forms, morph, alt_lemmas, paradigms).
package data

import "embed"

// FS contains the core compressed dictionary tables (lemmas, adj_noun, types, overrides, manifest).
//
//go:embed lemmas.tsv.gz types.tsv.gz adj_noun.tsv.gz overrides.tsv manifest.json
var FS embed.FS
