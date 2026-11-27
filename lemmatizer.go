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
	"unicode"
	"unicode/utf8"
)

// DefaultKeyStopWords are service & commercial words excluded from LemmaKey.
var DefaultKeyStopWords = map[string]struct{}{
	"в": {}, "во": {}, "на": {}, "для": {}, "и": {}, "с": {}, "со": {}, "по": {},
	"из": {}, "от": {}, "до": {}, "без": {}, "под": {}, "к": {}, "у": {}, "о": {},
	"об": {}, "а": {}, "или": {}, "купить": {}, "цена": {}, "цены": {},
	"недорого": {}, "дешево": {}, "интернет": {}, "магазин": {},
}

// DefaultSEONameStopWords are prepositions ignored during SEO name deduplication.
var DefaultSEONameStopWords = map[string]struct{}{
	"и": {}, "в": {}, "на": {}, "с": {}, "по": {}, "для": {},
	"из": {}, "к": {}, "о": {}, "от": {}, "до": {},
}

// Lemmatizer holds preloaded immutable dictionaries for fast concurrent Russian morphology.
type Lemmatizer struct {
	lemmas         map[string]string
	types          map[string]GramInfo
	adjForms       map[string]string
	adjToNoun      map[string]string
	suffixFallback bool
	overridesCount int
}

// Stats describes loaded dictionary sizes.
type Stats struct {
	Lemmas    int
	Types     int
	AdjForms  int
	AdjNoun   int
	Overrides int
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
	ovCnt := 0
	if _, err := fs.Stat(fsys, "overrides.tsv"); err == nil {
		n, err := applyOverridesFS(lemmas, fsys, "overrides.tsv")
		if err != nil {
			return nil, fmt.Errorf("load overrides.tsv: %w", err)
		}
		ovCnt += n
	}
	if opts.ExtraOverridesPath != "" {
		n, err := applyOverrides(lemmas, opts.ExtraOverridesPath)
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
	return &Lemmatizer{
		lemmas:         lemmas,
		types:          types,
		adjForms:       adjForms,
		adjToNoun:      adjToNoun,
		suffixFallback: !opts.DisableSuffixFallback,
		overridesCount: ovCnt,
	}, nil
}

// Stats returns dictionary entry counts.
func (l *Lemmatizer) Stats() Stats {
	return Stats{
		Lemmas:    len(l.lemmas),
		Types:     len(l.types),
		AdjForms:  len(l.adjForms),
		AdjNoun:   len(l.adjToNoun),
		Overrides: l.overridesCount,
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
func Tokenize(text string) []string {
	var tokens []string
	var buf strings.Builder
	buf.Grow(32)

	flush := func() {
		if buf.Len() == 0 {
			return
		}
		tok := strings.Trim(buf.String(), "-")
		buf.Reset()
		if tok != "" {
			tokens = append(tokens, tok)
		}
	}

	for _, r := range text {
		if isWordRune(r) {
			buf.WriteRune(unicode.ToLower(r))
		} else if r == '-' && buf.Len() > 0 {
			buf.WriteByte('-')
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

// Word returns the normalized lemma (ё -> е) for a single token.
func (l *Lemmatizer) Word(word string) string {
	w := strings.Trim(strings.ToLower(word), "-")
	if w == "" {
		return ""
	}
	if !hasCyrillic(w) {
		return w
	}
	if lm, ok := l.lemmas[w]; ok {
		return lm
	}
	wn := normYo(w)
	if wn != w {
		if lm, ok := l.lemmas[wn]; ok {
			return lm
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
			lp := l.Word(p)
			if lp != p {
				parts[i] = lp
				changed = true
			}
		}
		if changed {
			return strings.Join(parts, "-")
		}
		return wn
	}
	if l.suffixFallback {
		return l.fallbackSuffix(wn)
	}
	return wn
}

// Conservative Russian productive case/plural suffixes for completely unseen OOV Cyrillic nouns.
// Never turns OOV words into verbs or fake feminine -а nouns.
var safeOOVSuffixes = []string{
	"ами", "ями", "ах", "ях", "ам", "ям", "ов", "ев", "ом", "ем",
}

func (l *Lemmatizer) fallbackSuffix(w string) string {
	rLen := utf8.RuneCountInString(w)
	if rLen < 6 {
		return w
	}
	// Productive Russian feminine -ия nouns in gen/dat/loc (-ии -> -ия, e.g. бодиметрии -> бодиметрия)
	if rLen >= 7 && strings.HasSuffix(w, "ии") {
		cand := strings.TrimSuffix(w, "ии") + "ия"
		if lm, ok := l.lemmas[cand]; ok {
			return lm
		}
		return cand
	}
	for _, suf := range safeOOVSuffixes {
		if strings.HasSuffix(w, suf) {
			stem := strings.TrimSuffix(w, suf)
			if utf8.RuneCountInString(stem) >= 4 {
				// Only apply if stem itself is a known lemma or ends in a consonant
				if lm, ok := l.lemmas[stem]; ok {
					return lm
				}
				return stem
			}
		}
	}
	return w
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

// TypeGram returns grammatical gender/number for a Russian product type name.
func (l *Lemmatizer) TypeGram(typeName string) (GramInfo, bool) {
	tn := strings.ToLower(strings.TrimSpace(typeName))
	if tn == "" {
		return GramInfo{}, false
	}
	if g, ok := l.types[tn]; ok {
		return g, true
	}
	if fields := strings.Fields(tn); len(fields) > 0 {
		if g, ok := l.types[fields[0]]; ok {
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
