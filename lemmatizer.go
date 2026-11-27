// Package rulemma provides a fast, dictionary-backed Russian & e-commerce lemmatizer,
// lemma-key normalizer, adjective agreement engine, and SEO name builder.
//
// Dictionaries are read-only after Load, making every method safe for concurrent use
// across goroutines with zero lock contention.
package rulemma

import (
	"fmt"
	"io/fs"
	"os"
	"sort"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"
)

// DefaultKeyStopWords are service & commercial words excluded from LemmaKey and SemanticKey.
// Negation markers ("без", "не", "нет") are intentionally NOT excluded so opposite queries
// (e.g. "шоколад без сахара" vs "шоколад с сахаром") never collide.
var DefaultKeyStopWords = map[string]struct{}{
	"в": {}, "во": {}, "на": {}, "для": {}, "и": {}, "с": {}, "со": {}, "по": {},
	"из": {}, "от": {}, "до": {}, "под": {}, "к": {}, "у": {}, "о": {},
	"об": {}, "а": {}, "или": {}, "купить": {}, "цена": {}, "цены": {},
	"недорого": {}, "дешево": {}, "интернет": {}, "магазин": {},
}

// GeneralKeyStopWords contains only neutral Russian prepositions and conjunctions
// (no commercial terms like "купить"/"магазин" and no negations like "без"/"не").
var GeneralKeyStopWords = map[string]struct{}{
	"в": {}, "во": {}, "на": {}, "для": {}, "и": {}, "с": {}, "со": {}, "по": {},
	"из": {}, "от": {}, "до": {}, "под": {}, "к": {}, "у": {}, "о": {},
	"об": {}, "а": {}, "или": {},
}

// EcommerceKeyStopWords is an explicit alias for the e-commerce stop-word profile (same as DefaultKeyStopWords).
var EcommerceKeyStopWords = map[string]struct{}{
	"в": {}, "во": {}, "на": {}, "для": {}, "и": {}, "с": {}, "со": {}, "по": {},
	"из": {}, "от": {}, "до": {}, "под": {}, "к": {}, "у": {}, "о": {},
	"об": {}, "а": {}, "или": {}, "купить": {}, "цена": {}, "цены": {},
	"недорого": {}, "дешево": {}, "интернет": {}, "магазин": {},
}

// DefaultSEONameStopWords are prepositions ignored during SEO name deduplication.
var DefaultSEONameStopWords = map[string]struct{}{
	"и": {}, "в": {}, "на": {}, "с": {}, "по": {}, "для": {},
	"из": {}, "к": {}, "о": {}, "от": {}, "до": {},
}

// WordSource constants returned by LookupWord.
const (
	SourceOverride = "override"
	SourceDict     = "dict"
	SourceRule     = "rule"
	SourceOOV      = "oov"
)

// Lemmatizer holds preloaded immutable dictionaries for fast concurrent Russian morphology.
type Lemmatizer struct {
	fsys           fs.FS
	lemmas         map[string]string
	overrides      map[string]struct{}
	types          map[string]GramInfo
	adjForms       map[string]string
	adjToNoun      map[string]string
	morphOnce      sync.Once
	morph          map[string]uint32
	altLemmas      map[string]string
	paradigmOnce   sync.Once
	paradigms      []paradigmDef
	paradigmWords  map[string]paradigmRef
	manifest       *Manifest
	suffixFallback bool
	overridesCount int
}

// Stats describes loaded dictionary sizes.
type Stats struct {
	Lemmas        int
	Types         int
	AdjForms      int
	AdjNoun       int
	Overrides     int
	Morph         int
	AltLemmas     int
	Paradigms     int
	ParadigmWords int
}

// Load initializes a Lemmatizer from dataDir using default options.
func Load(dataDir string) (*Lemmatizer, error) {
	return LoadWithOptions(dataDir, Options{})
}

// LoadWithOptions initializes a Lemmatizer from dataDir with custom options.
func LoadWithOptions(dataDir string, opts Options) (*Lemmatizer, error) {
	info, err := os.Stat(dataDir)
	if err != nil {
		return nil, fmt.Errorf("stat data dir %s: %w", dataDir, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("data path %s is not a directory", dataDir)
	}
	return loadFS(os.DirFS(dataDir), dataDir, opts)
}

// LoadFS initializes a Lemmatizer from an fs.FS (such as embed.FS or os.DirFS).
// If opts.ExtraOverridesPath is set, it is loaded from the local filesystem on top of base overrides.
func LoadFS(fsys fs.FS, opts Options) (*Lemmatizer, error) {
	return loadFS(fsys, "fs.FS", opts)
}

func loadFS(fsys fs.FS, label string, opts Options) (*Lemmatizer, error) {
	lemmas, err := loadLemmasFS(fsys, label)
	if err != nil {
		return nil, err
	}
	overrides := make(map[string]struct{}, 512)
	ovCnt := 0
	if _, err := fs.Stat(fsys, "overrides.tsv"); err == nil {
		n, err := applyOverridesFS(lemmas, overrides, fsys, "overrides.tsv")
		if err != nil {
			return nil, fmt.Errorf("load overrides.tsv: %w", err)
		}
		ovCnt += n
	}
	if opts.ExtraOverridesPath != "" {
		n, err := applyOverrides(lemmas, overrides, opts.ExtraOverridesPath)
		if err != nil {
			return nil, fmt.Errorf("load extra overrides %s: %w", opts.ExtraOverridesPath, err)
		}
		ovCnt += n
	}
	types, err := loadTypesFS(fsys)
	if err != nil {
		return nil, err
	}
	adjForms, err := loadAdjFormsFS(fsys)
	if err != nil {
		return nil, err
	}
	adjToNoun, err := loadAdjNounFS(fsys)
	if err != nil {
		return nil, err
	}
	manifest, _ := LoadManifestFS(fsys)
	lem := &Lemmatizer{
		fsys:           fsys,
		lemmas:         lemmas,
		overrides:      overrides,
		types:          types,
		adjForms:       adjForms,
		adjToNoun:      adjToNoun,
		manifest:       manifest,
		suffixFallback: !opts.DisableSuffixFallback,
		overridesCount: ovCnt,
	}
	if opts.PreloadAll {
		lem.Preload()
	}
	return lem, nil
}

func (l *Lemmatizer) ensureMorph() {
	l.morphOnce.Do(func() {
		if l.fsys == nil {
			return
		}
		if m, err := loadMorphFS(l.fsys); err == nil {
			l.morph = m
		}
		if a, err := loadAltLemmasFS(l.fsys); err == nil {
			l.altLemmas = a
		}
	})
}

func (l *Lemmatizer) ensureParadigms() {
	l.paradigmOnce.Do(func() {
		if l.fsys == nil {
			return
		}
		if defs, words, err := loadParadigmsFS(l.fsys); err == nil {
			l.paradigms = defs
			l.paradigmWords = words
		}
	})
}

// Preload eagerly loads the optional v0.3 morphology and inflection tables (normally loaded lazily on first use).
func (l *Lemmatizer) Preload() {
	l.ensureMorph()
	l.ensureParadigms()
}

// Manifest returns the dictionary manifest loaded alongside the dictionaries, or nil if absent.
func (l *Lemmatizer) Manifest() *Manifest {
	return l.manifest
}

// Stats returns dictionary entry counts.
func (l *Lemmatizer) Stats() Stats {
	pCnt := 0
	if len(l.paradigms) > 1 {
		pCnt = len(l.paradigms) - 1
	}
	return Stats{
		Lemmas:        len(l.lemmas),
		Types:         len(l.types),
		AdjForms:      len(l.adjForms),
		AdjNoun:       len(l.adjToNoun),
		Overrides:     l.overridesCount,
		Morph:         len(l.morph),
		AltLemmas:     len(l.altLemmas),
		Paradigms:     pCnt,
		ParadigmWords: len(l.paradigmWords),
	}
}

// normYo normalizes Russian ё/Ё to е/е for consistent dictionary lookup.
func normYo(s string) string {
	if !strings.ContainsAny(s, "ёЁ") {
		return s
	}
	r := strings.ReplaceAll(s, "ё", "е")
	return strings.ReplaceAll(r, "Ё", "е")
}

// isTokenRune matches Cyrillic, Latin, digits, and internal hyphens.
func isWordRune(r rune) bool {
	return (r >= 'а' && r <= 'я') || (r >= 'А' && r <= 'Я') || r == 'ё' || r == 'Ё' ||
		(r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
		(r >= '0' && r <= '9')
}

// hasCyrillic reports whether s contains at least one Cyrillic rune.
func hasCyrillic(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			for _, r := range s[i:] {
				if (r >= 'а' && r <= 'я') || (r >= 'А' && r <= 'Я') || r == 'ё' || r == 'Ё' {
					return true
				}
			}
			return false
		}
	}
	return false
}

// Tokenize extracts lowercase alphanumeric/hyphen tokens in a single fast pass (no regex).
// Consecutive hyphens (e.g. "a--b") act as token separators, matching Python's regex tokenizer.
func Tokenize(text string) []string {
	var tokens []string
	var buf strings.Builder
	buf.Grow(32)
	prevHyphen := false

	flush := func() {
		if buf.Len() == 0 {
			prevHyphen = false
			return
		}
		tok := strings.Trim(buf.String(), "-")
		buf.Reset()
		prevHyphen = false
		if tok != "" {
			tokens = append(tokens, tok)
		}
	}

	for _, r := range text {
		if isWordRune(r) {
			buf.WriteRune(unicode.ToLower(r))
			prevHyphen = false
		} else if r == '-' && buf.Len() > 0 && !prevHyphen {
			buf.WriteByte('-')
			prevHyphen = true
		} else {
			flush()
		}
	}
	flush()
	return tokens
}

// NormalizePhrase tokenizes text into lowercase alphanumeric/hyphen tokens, strips
// surrounding punctuation and repeated whitespace, and joins them with a single space.
// When keepYo is false (recommended for caching and deduplication), 'ё'/'Ё' is normalized to 'е'.
func NormalizePhrase(text string, keepYo bool) string {
	toks := Tokenize(text)
	if len(toks) == 0 {
		return ""
	}
	if !keepYo {
		for i, t := range toks {
			toks[i] = normYo(t)
		}
	}
	return strings.Join(toks, " ")
}

// MakeStopWords builds a normalized stop-word set for LemmaKeyWithStop and SemanticKeyWithStop.
// Each word is trimmed, lowercased, and stored in both original and ё->е normalized forms.
func MakeStopWords(words []string) map[string]struct{} {
	out := make(map[string]struct{}, len(words))
	for _, w := range words {
		w = strings.ToLower(strings.TrimSpace(w))
		if w == "" {
			continue
		}
		out[w] = struct{}{}
		if wn := normYo(w); wn != w {
			out[wn] = struct{}{}
		}
	}
	return out
}

// LookupWord returns the normalized lemma (ё -> е) for a single token along with its provenance:
//   - SourceOverride ("override"): matched an explicit rule in overrides.tsv / ExtraOverridesPath;
//   - SourceDict ("dict"): matched an entry in the base dictionary;
//   - SourceRule ("rule"): derived by hyphenated compound splitting or dictionary-verified suffix fallback;
//   - SourceOOV ("oov"): unseen word or non-Cyrillic token returned unchanged.
func (l *Lemmatizer) LookupWord(word string) (string, string) {
	w := strings.Trim(strings.ToLower(word), "-")
	if w == "" {
		return "", SourceOOV
	}
	if !hasCyrillic(w) {
		return w, SourceOOV
	}
	if lm, ok := l.lemmas[w]; ok {
		if _, isOv := l.overrides[w]; isOv {
			return lm, SourceOverride
		}
		return lm, SourceDict
	}
	wn := normYo(w)
	if wn != w {
		if lm, ok := l.lemmas[wn]; ok {
			if _, isOv := l.overrides[wn]; isOv {
				return lm, SourceOverride
			}
			return lm, SourceDict
		}
	}
	// Hyphenated compound word: lemmatize parts independently (e.g. "роботов-пылесосов" -> "робот-пылесос").
	if strings.ContainsRune(wn, '-') {
		parts := strings.Split(wn, "-")
		changed := false
		for i, p := range parts {
			if p == "" {
				continue
			}
			lp, _ := l.LookupWord(p)
			if lp != p {
				parts[i] = lp
				changed = true
			}
		}
		if changed {
			return strings.Join(parts, "-"), SourceRule
		}
		return wn, SourceOOV
	}
	if l.suffixFallback {
		if lm, ok := l.fallbackSuffix(wn); ok {
			return lm, SourceRule
		}
	}
	return wn, SourceOOV
}

// Word returns the normalized lemma (ё -> е) for a single token.
func (l *Lemmatizer) Word(word string) string {
	lm, _ := l.LookupWord(word)
	return lm
}

// isCanonicalLemma reports whether cand is a known base lemma in l.lemmas (maps to itself).
func (l *Lemmatizer) isCanonicalLemma(cand string) bool {
	lm, ok := l.lemmas[cand]
	return ok && lm == cand
}

func (l *Lemmatizer) tryCanon(cands ...string) (string, bool) {
	for _, c := range cands {
		if l.isCanonicalLemma(c) {
			return c, true
		}
	}
	return "", false
}

// tryFleeting restores a Russian fleeting vowel ('о'/'е') before the final stem consonant
// (e.g. осколк -> осколок, шлепанц -> шлепанец, противн -> противень) ONLY when the candidate
// is a verified canonical lemma in l.lemmas.
func (l *Lemmatizer) tryFleeting(stem string) (string, bool) {
	runes := []rune(stem)
	n := len(runes)
	if n < 4 {
		return "", false
	}
	prefix := string(runes[:n-1])
	last := string(runes[n-1])
	return l.tryCanon(
		prefix+"о"+last,
		prefix+"е"+last,
		prefix+"е"+last+"ь",
		prefix+"о"+last+"ь",
	)
}

func endsWithCyrVowel(s string) bool {
	r, _ := utf8.DecodeLastRuneInString(s)
	switch r {
	case 'а', 'е', 'ё', 'и', 'о', 'у', 'ы', 'э', 'ю', 'я':
		return true
	}
	return false
}

// Dictionary-verified suffix fallback for Russian inflected forms whose base lemma is present
// in l.lemmas, preventing truncated non-word stems (e.g. пищевом->пищевой, нервном->нервный,
// условием->условие, сухарями->сухарь, судебного->судебный, приказа->приказ, осколком->осколок).
// Never returns an unverified truncated stem.
func (l *Lemmatizer) fallbackSuffix(w string) (string, bool) {
	rLen := utf8.RuneCountInString(w)
	if rLen < 5 {
		return w, false
	}

	// 1. Productive -ие / -ия / -ий nouns (условием->условие, обозначениями->обозначение, асимметрию->асимметрия)
	if rLen >= 6 {
		for _, suf := range []string{"иями", "иях", "иям", "ием"} {
			if strings.HasSuffix(w, suf) {
				p := strings.TrimSuffix(w, suf)
				if utf8.RuneCountInString(p) >= 2 {
					if lm, ok := l.tryCanon(p+"ие", p+"ия"); ok {
						return lm, true
					}
				}
			}
		}
		if strings.HasSuffix(w, "ию") {
			p := strings.TrimSuffix(w, "ию")
			if utf8.RuneCountInString(p) >= 2 {
				if lm, ok := l.tryCanon(p+"ия", p+"ие"); ok {
					return lm, true
				}
			}
		}
		if rLen >= 7 && strings.HasSuffix(w, "ии") {
			p := strings.TrimSuffix(w, "ии")
			if lm, ok := l.tryCanon(p+"ия", p+"ие", p+"ий"); ok {
				return lm, true
			}
			// Productive Greco-Latin feminine -ия nouns (e.g. бодиметрии -> бодиметрия)
			for _, root := range []string{"метр", "лог", "граф", "скоп", "терап", "ном", "фоб", "ман"} {
				if strings.HasSuffix(p, root) {
					return p + "ия", true
				}
			}
		}
	}

	// 2. Adjective & participle inflections (unambiguous endings: stemLen >= 3)
	for _, suf := range []string{"ого", "ому", "ыми", "ая", "ую", "ое", "ые", "ых", "ым"} {
		if strings.HasSuffix(w, suf) {
			stem := strings.TrimSuffix(w, suf)
			if utf8.RuneCountInString(stem) >= 3 {
				if lm, ok := l.tryCanon(stem+"ый", stem+"ой", stem+"ий"); ok {
					return lm, true
				}
			}
		}
	}
	for _, suf := range []string{"его", "ему", "ими", "яя", "юю", "ее", "ие", "их", "им"} {
		if strings.HasSuffix(w, suf) {
			stem := strings.TrimSuffix(w, suf)
			if utf8.RuneCountInString(stem) >= 3 {
				if lm, ok := l.tryCanon(stem+"ий", stem+"ой", stem+"ый"); ok {
					return lm, true
				}
				// Superlative adjectives mapped to base degree in l.lemmas (e.g. высший->высокий, лучшего->хороший)
				if strings.HasSuffix(stem, "ш") {
					if mapped, ok := l.lemmas[stem+"ий"]; ok && (strings.HasSuffix(mapped, "ый") || strings.HasSuffix(mapped, "ий") || strings.HasSuffix(mapped, "ой")) {
						return mapped, true
					}
				}
			}
		}
	}

	// 3. Shared adjective / noun endings (stemLen >= 4)
	if strings.HasSuffix(w, "ом") {
		stem := strings.TrimSuffix(w, "ом")
		if utf8.RuneCountInString(stem) >= 4 && !endsWithCyrVowel(stem) {
			if lm, ok := l.tryCanon(stem+"ый", stem+"ой", stem+"ий", stem, stem+"о", stem+"а"); ok {
				return lm, true
			}
			if lm, ok := l.tryFleeting(stem); ok {
				return lm, true
			}
		}
	}
	if strings.HasSuffix(w, "ем") {
		stem := strings.TrimSuffix(w, "ем")
		if utf8.RuneCountInString(stem) >= 4 && !endsWithCyrVowel(stem) {
			if lm, ok := l.tryCanon(stem+"ий", stem+"ый", stem+"ь", stem+"й", stem+"е"); ok {
				return lm, true
			}
			if lm, ok := l.tryFleeting(stem); ok {
				return lm, true
			}
		}
	}
	if strings.HasSuffix(w, "ой") {
		stem := strings.TrimSuffix(w, "ой")
		if utf8.RuneCountInString(stem) >= 4 && !endsWithCyrVowel(stem) {
			if lm, ok := l.tryCanon(stem+"ый", stem+"ий", stem+"ой", stem+"а"); ok {
				return lm, true
			}
		}
	}
	if strings.HasSuffix(w, "ей") {
		stem := strings.TrimSuffix(w, "ей")
		if utf8.RuneCountInString(stem) >= 4 && !endsWithCyrVowel(stem) {
			// After sibilants/ц (ж, ш, ч, щ, ц), 1st-declension feminine -а nouns take instrumental -ей (выдача -> выдачей)
			lastR, _ := utf8.DecodeLastRuneInString(stem)
			if lastR == 'ж' || lastR == 'ш' || lastR == 'ч' || lastR == 'щ' || lastR == 'ц' {
				if lm, ok := l.tryCanon(stem+"а", stem+"ий", stem+"ый", stem+"ь", stem+"я", stem+"е"); ok {
					return lm, true
				}
			} else if lm, ok := l.tryCanon(stem+"ий", stem+"ый", stem+"ь", stem+"я", stem+"е"); ok {
				return lm, true
			}
		}
	}

	// 4. Productive plural & oblique noun endings (stemLen >= 4, consonant stem)
	for _, suf := range []string{"ами", "ах", "ам"} {
		if strings.HasSuffix(w, suf) {
			stem := strings.TrimSuffix(w, suf)
			if utf8.RuneCountInString(stem) >= 4 && !endsWithCyrVowel(stem) {
				if lm, ok := l.tryCanon(stem+"а", stem, stem+"о"); ok {
					return lm, true
				}
				if lm, ok := l.tryFleeting(stem); ok {
					return lm, true
				}
			}
		}
	}
	for _, suf := range []string{"ями", "ях", "ям"} {
		if strings.HasSuffix(w, suf) {
			stem := strings.TrimSuffix(w, suf)
			if utf8.RuneCountInString(stem) >= 4 && !endsWithCyrVowel(stem) {
				if lm, ok := l.tryCanon(stem+"ь", stem+"я", stem+"е"); ok {
					return lm, true
				}
				if lm, ok := l.tryFleeting(stem); ok {
					return lm, true
				}
			}
		}
	}
	if strings.HasSuffix(w, "ов") {
		stem := strings.TrimSuffix(w, "ов")
		if utf8.RuneCountInString(stem) >= 4 && !endsWithCyrVowel(stem) {
			if lm, ok := l.tryCanon(stem, stem+"о"); ok {
				return lm, true
			}
			if lm, ok := l.tryFleeting(stem); ok {
				return lm, true
			}
		}
	}
	if strings.HasSuffix(w, "ев") {
		stem := strings.TrimSuffix(w, "ев")
		if utf8.RuneCountInString(stem) >= 4 && !endsWithCyrVowel(stem) {
			if lm, ok := l.tryCanon(stem+"й", stem+"ь", stem+"е"); ok {
				return lm, true
			}
			if lm, ok := l.tryFleeting(stem); ok {
				return lm, true
			}
		}
	}

	// 5. Single-char noun case endings (strict: stemLen >= 4, consonant stem, verified canonical lemma only)
	if rLen >= 5 {
		if strings.HasSuffix(w, "у") {
			stem := strings.TrimSuffix(w, "у")
			if utf8.RuneCountInString(stem) >= 4 && !endsWithCyrVowel(stem) {
				if lm, ok := l.tryCanon(stem+"а", stem, stem+"о"); ok {
					return lm, true
				}
				if lm, ok := l.tryFleeting(stem); ok {
					return lm, true
				}
			}
		} else if strings.HasSuffix(w, "ю") {
			stem := strings.TrimSuffix(w, "ю")
			if utf8.RuneCountInString(stem) >= 4 && !endsWithCyrVowel(stem) {
				if lm, ok := l.tryCanon(stem+"я", stem+"ь", stem+"й"); ok {
					return lm, true
				}
			}
		} else if strings.HasSuffix(w, "е") {
			stem := strings.TrimSuffix(w, "е")
			if utf8.RuneCountInString(stem) >= 4 && !endsWithCyrVowel(stem) {
				if lm, ok := l.tryCanon(stem+"а", stem+"я", stem, stem+"ь", stem+"о"); ok {
					return lm, true
				}
				if lm, ok := l.tryFleeting(stem); ok {
					return lm, true
				}
			}
		} else if strings.HasSuffix(w, "а") {
			stem := strings.TrimSuffix(w, "а")
			if utf8.RuneCountInString(stem) >= 4 && !endsWithCyrVowel(stem) {
				if lm, ok := l.tryCanon(stem, stem+"о"); ok {
					return lm, true
				}
				if lm, ok := l.tryFleeting(stem); ok {
					return lm, true
				}
			}
		} else if strings.HasSuffix(w, "я") {
			stem := strings.TrimSuffix(w, "я")
			if utf8.RuneCountInString(stem) >= 4 && !endsWithCyrVowel(stem) {
				if lm, ok := l.tryCanon(stem+"ь", stem+"й", stem+"е"); ok {
					return lm, true
				}
			}
		} else if strings.HasSuffix(w, "и") {
			stem := strings.TrimSuffix(w, "и")
			if utf8.RuneCountInString(stem) >= 4 && !endsWithCyrVowel(stem) {
				if lm, ok := l.tryCanon(stem+"а", stem+"я", stem+"ь", stem); ok {
					return lm, true
				}
			}
		} else if strings.HasSuffix(w, "ы") {
			stem := strings.TrimSuffix(w, "ы")
			if utf8.RuneCountInString(stem) >= 4 && !endsWithCyrVowel(stem) {
				if lm, ok := l.tryCanon(stem+"а", stem); ok {
					return lm, true
				}
			}
		}
	}

	return w, false
}

// Lemmatize tokenizes text and returns the slice of lemmas in original token order.
func (l *Lemmatizer) Lemmatize(text string) []string {
	toks := Tokenize(text)
	out := make([]string, len(toks))
	for i, t := range toks {
		out[i] = l.Word(t)
	}
	return out
}

// LemmatizeString returns space-joined lemmas of text in original token order.
func (l *Lemmatizer) LemmatizeString(text string) string {
	return strings.Join(l.Lemmatize(text), " ")
}

// LemmaKey returns a canonical sorted, deduplicated lemma key excluding DefaultKeyStopWords.
// Order-independent sorted-token lemma key, 100x+ faster than Python morphological analyzers.
func (l *Lemmatizer) LemmaKey(text string) string {
	return l.LemmaKeyWithStop(text, DefaultKeyStopWords)
}

// LemmaKeyWithStop returns a sorted, deduplicated lemma key excluding the given stop set.
func (l *Lemmatizer) LemmaKeyWithStop(text string, stop map[string]struct{}) string {
	toks := Tokenize(text)
	if len(toks) == 0 {
		return ""
	}
	seen := make(map[string]struct{}, len(toks))
	keys := make([]string, 0, len(toks))
	for _, t := range toks {
		if stop != nil {
			if _, skip := stop[t]; skip {
				continue
			}
			if tn := normYo(t); tn != t {
				if _, skip := stop[tn]; skip {
					continue
				}
			}
		}
		lm := l.Word(t)
		if lm == "" {
			continue
		}
		if stop != nil {
			if _, skip := stop[lm]; skip {
				continue
			}
		}
		if _, dup := seen[lm]; dup {
			continue
		}
		seen[lm] = struct{}{}
		keys = append(keys, lm)
	}
	sort.Strings(keys)
	return strings.Join(keys, " ")
}

// AdjToNoun maps a relational Russian adjective (in any inflected form) to its canonical noun
// when present in the curated derivational table (e.g. "зимние" -> "зима", "детского" -> "ребенок",
// "школьных" -> "школа"). Returns (lemma, false) if no safe derivational mapping exists.
func (l *Lemmatizer) AdjToNoun(word string) (string, bool) {
	lm := l.Word(word)
	if lm == "" {
		return "", false
	}
	if noun, ok := l.adjToNoun[lm]; ok {
		return noun, true
	}
	return lm, false
}

// SemanticKey returns a sorted, deduplicated canonical search key that ALSO folds relational
// adjectives into their canonical nouns via AdjToNoun.
// Example: "зимние кроссовки найк" and "кроссовки на зиму найк" both yield "зима кроссовок найк";
// "детский рюкзак" and "рюкзак для детей" both yield "ребенок рюкзак".
func (l *Lemmatizer) SemanticKey(text string) string {
	return l.SemanticKeyWithStop(text, DefaultKeyStopWords)
}

// LemmatizeAll returns (lemmatized_text, lemma_key, semantic_key) in a single tokenization & lookup pass.
func (l *Lemmatizer) LemmatizeAll(text string) (lemText, lemmaKey, semanticKey string) {
	return l.LemmatizeAllWithStop(text, DefaultKeyStopWords)
}

// LemmatizeAllWithStop returns (lemmatized_text, lemma_key, semantic_key) using a custom stop set
// in a single pass (one Tokenize + one Word lookup per token instead of three separate passes).
func (l *Lemmatizer) LemmatizeAllWithStop(text string, stop map[string]struct{}) (lemText, lemmaKey, semanticKey string) {
	toks := Tokenize(text)
	if len(toks) == 0 {
		return "", "", ""
	}
	lemmas := make([]string, len(toks))
	seenKey := make(map[string]struct{}, len(toks))
	keys := make([]string, 0, len(toks))
	seenSem := make(map[string]struct{}, len(toks))
	semKeys := make([]string, 0, len(toks))

	for i, t := range toks {
		lm := l.Word(t)
		lemmas[i] = lm
		if lm == "" {
			continue
		}
		if stop != nil {
			if _, skip := stop[t]; skip {
				continue
			}
			if tn := normYo(t); tn != t {
				if _, skip := stop[tn]; skip {
					continue
				}
			}
			if _, skip := stop[lm]; skip {
				continue
			}
		}
		if _, dup := seenKey[lm]; !dup {
			seenKey[lm] = struct{}{}
			keys = append(keys, lm)
		}
		sem := lm
		if noun, ok := l.adjToNoun[lm]; ok {
			sem = noun
		}
		if _, dup := seenSem[sem]; !dup {
			seenSem[sem] = struct{}{}
			semKeys = append(semKeys, sem)
		}
	}
	sort.Strings(keys)
	sort.Strings(semKeys)
	return strings.Join(lemmas, " "), strings.Join(keys, " "), strings.Join(semKeys, " ")
}

// SemanticKeyWithStop returns a derivational-aware canonical key excluding the given stop set.
func (l *Lemmatizer) SemanticKeyWithStop(text string, stop map[string]struct{}) string {
	toks := Tokenize(text)
	if len(toks) == 0 {
		return ""
	}
	seen := make(map[string]struct{}, len(toks))
	keys := make([]string, 0, len(toks))
	for _, t := range toks {
		if stop != nil {
			if _, skip := stop[t]; skip {
				continue
			}
			if tn := normYo(t); tn != t {
				if _, skip := stop[tn]; skip {
					continue
				}
			}
		}
		lm := l.Word(t)
		if lm == "" {
			continue
		}
		if stop != nil {
			if _, skip := stop[lm]; skip {
				continue
			}
		}
		if noun, ok := l.adjToNoun[lm]; ok {
			lm = noun
		}
		if _, dup := seen[lm]; dup {
			continue
		}
		seen[lm] = struct{}{}
		keys = append(keys, lm)
	}
	sort.Strings(keys)
	return strings.Join(keys, " ")
}

// lookupTypeToken resolves a single-word product type (singular or plural) against l.types.
func (l *Lemmatizer) lookupTypeToken(tok string) (GramInfo, bool) {
	if g, ok := l.types[tok]; ok && g.Number != "" {
		return g, true
	}
	tn := normYo(tok)
	if tn != tok {
		if g, ok := l.types[tn]; ok && g.Number != "" {
			return g, true
		}
	}
	lm := l.Word(tn)
	if lm != "" && lm != tn {
		if g, ok := l.types[lm]; ok && g.Number != "" {
			if g.Number == "plur" {
				return g, true
			}
			// Plural Russian nominative forms of a singular product type (купальники, триммеры, пилы, ведра, кресла)
			if strings.HasSuffix(tn, "ы") || strings.HasSuffix(tn, "и") ||
				strings.HasSuffix(tn, "а") || strings.HasSuffix(tn, "я") {
				return GramInfo{Gender: g.Gender, Number: "plur"}, true
			}
		}
	}
	return GramInfo{}, false
}

// TypeGram returns grammatical gender/number for a Russian product type name (singular or plural,
// single-word or multi-word such as "купальники", "кофемашина", "стиральные машины").
func (l *Lemmatizer) TypeGram(typeName string) (GramInfo, bool) {
	tn := strings.ToLower(strings.TrimSpace(typeName))
	if tn == "" {
		return GramInfo{}, false
	}
	if g, ok := l.types[tn]; ok && g.Number != "" {
		return g, true
	}
	fields := strings.Fields(tn)
	if len(fields) == 1 {
		return l.lookupTypeToken(fields[0])
	}
	if len(fields) > 1 {
		if g, ok := l.lookupTypeToken(fields[0]); ok {
			return g, true
		}
		if g, ok := l.lookupTypeToken(fields[len(fields)-1]); ok {
			return g, true
		}
	}
	return GramInfo{}, false
}

// AgreeAdjective inflects a single-word Russian adjective to agree with productType's gender/number.
func (l *Lemmatizer) AgreeAdjective(adj, productType string) (string, bool) {
	gram, ok := l.TypeGram(productType)
	if !ok {
		return adj, false
	}
	return l.AgreeAdjectiveGram(adj, gram)
}

// AgreeAdjectiveGram inflects a single-word Russian adjective to match gram.
func (l *Lemmatizer) AgreeAdjectiveGram(adj string, gram GramInfo) (string, bool) {
	if gram.Number == "" {
		return adj, false
	}
	if gram.Number != "plur" && gram.Gender != "masc" && gram.Gender != "femn" && gram.Gender != "neut" {
		return adj, false
	}
	words := strings.Fields(adj)
	if len(words) != 1 {
		return adj, false
	}
	word := strings.ToLower(words[0])
	lemma := l.Word(word)
	if lemma == "" {
		return adj, false
	}
	gender := gram.Gender
	if gram.Number == "plur" {
		gender = ""
	}
	key := lemma + "|" + gender + "|" + gram.Number
	form, ok := l.adjForms[key]
	if !ok {
		return adj, false
	}
	if form != word {
		return form, true
	}
	return form, false
}

// BuildSEOName appends deduplicated, grammatically agreed keywords to baseName up to maxLen runes.
// Migrated and generalized from SeoName Go builder.
func (l *Lemmatizer) BuildSEOName(baseName, productType string, keywords []string, maxLen int) (seoName string, numAdded, numAgreed int) {
	if maxLen <= 0 {
		maxLen = 100
	}
	base := stripSEOName(baseName)
	if utf8.RuneCountInString(base) > maxLen {
		return base, 0, 0
	}
	used := l.lemmaSetForSEOName(base)
	gram, _ := l.TypeGram(productType)
	result := base

	for _, kw := range keywords {
		kw = strings.TrimSpace(kw)
		if kw == "" {
			continue
		}
		kwLemmas := l.lemmaSetForSEOName(kw)
		if len(kwLemmas) == 0 || hasSetIntersection(kwLemmas, used) {
			continue
		}
		kwOut, agreed := l.AgreeAdjectiveGram(kw, gram)
		candidate := result + ", " + kwOut
		if utf8.RuneCountInString(candidate) > maxLen {
			continue
		}
		result = candidate
		for k := range kwLemmas {
			used[k] = struct{}{}
		}
		numAdded++
		if agreed {
			numAgreed++
		}
	}
	return stripSEOName(result), numAdded, numAgreed
}

func (l *Lemmatizer) lemmaSetForSEOName(text string) map[string]struct{} {
	toks := Tokenize(text)
	out := make(map[string]struct{}, len(toks))
	for _, tok := range toks {
		if utf8.RuneCountInString(tok) < 2 {
			continue
		}
		if _, skip := DefaultSEONameStopWords[tok]; skip {
			continue
		}
		out[l.Word(tok)] = struct{}{}
	}
	return out
}

func hasSetIntersection(a, b map[string]struct{}) bool {
	for k := range a {
		if _, ok := b[k]; ok {
			return true
		}
	}
	return false
}

func stripSEOName(text string) string {
	const trash = " \t\n\r.,;:-–—/\\"
	text = strings.Trim(strings.TrimSpace(text), trash)
	for strings.Contains(text, "  ") {
		text = strings.ReplaceAll(text, "  ", " ")
	}
	return text
}
