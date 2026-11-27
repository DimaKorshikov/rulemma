// Package embedded provides one-line initialization of rulemma using dictionaries
// embedded directly into the Go binary via //go:embed.
//
// Importing this subpackage adds ~1.5 MB of compressed dictionary files to your binary.
// If you prefer loading dictionaries from disk at runtime, use rulemma.Load directly.
package embedded

import (
	"github.com/DimaKorshikov/rulemma"
	"github.com/DimaKorshikov/rulemma/data"
)

// Load initializes a Lemmatizer from the embedded dictionaries with default options.
func Load() (*rulemma.Lemmatizer, error) {
	return rulemma.LoadFS(data.FS, rulemma.Options{})
}

// LoadWithOptions initializes a Lemmatizer from the embedded dictionaries with custom options
// (for example, ExtraOverridesPath loaded from disk on top of the embedded base overrides).
func LoadWithOptions(opts rulemma.Options) (*rulemma.Lemmatizer, error) {
	return rulemma.LoadFS(data.FS, opts)
}

// Manifest returns metadata and SHA-256 checksums for the embedded dictionaries.
func Manifest() (*rulemma.Manifest, error) {
	return rulemma.LoadManifestFS(data.FS)
}
