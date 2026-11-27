//go:build !rulemma_core

// Package data exports the embedded base dictionaries for rulemma/embedded.
package data

import "embed"

// FS contains all compressed dictionary tables, overrides.tsv, and manifest.json (~2.6 MB).
// Build with `-tags rulemma_core` to embed only the lightweight core tables (~1.2 MB).
//
//go:embed lemmas.tsv.gz adj_forms.tsv.gz types.tsv.gz adj_noun.tsv.gz morph.tsv.gz alt_lemmas.tsv.gz paradigms.tsv.gz overrides.tsv manifest.json
var FS embed.FS
