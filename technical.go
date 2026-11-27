package rulemma

import (
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// TokenizerOptions configures optional v0.3 technical & e-commerce tokenization behavior.
// Zero value preserves conservative defaults; use TechnicalTokenizerOptions() for the full technical profile.
type TokenizerOptions struct {
	// NormalizeHomoglyphs fixes mixed Cyrillic+Latin lookalike letters inside a single token
	// (e.g. "cм" -> "см", "рrо" -> "pro", "intеl" -> "intel", "аsus" -> "asus").
	NormalizeHomoglyphs bool
	// SplitHyphens splits purely alphabetic hyphenated words ("онлайн-курсы" -> "онлайн", "курсы")
	// while keeping numeric ranges ("10-20") and model codes intact.
	SplitHyphens bool
	// SplitNumberUnit splits attached number+unit pairs ("5мм" -> "5", "мм"; "12в" -> "12", "в")
	// without breaking alphanumeric model codes like "3d", "4k", "5g", "55c6k".
	SplitNumberUnit bool
	// CompactCodes merges letter-digit model code tokens separated by hyphen or space
	// (e.g. "p-6000" / "p 6000" -> "p6000", "мб-23" / "мб 23" -> "мб23", "ecam 220" -> "ecam220").
	CompactCodes bool
}

// TechnicalTokenizerOptions returns the standard v0.3 technical e-commerce tokenizer options.
func TechnicalTokenizerOptions() TokenizerOptions {
	return TokenizerOptions{
		NormalizeHomoglyphs: true,
		SplitNumberUnit:     true,
	}
}

// KeyOptions configures Lemmatizer.KeyWithOptions (v0.3 opt-in profiles; default LemmaKey/SemanticKey stay unchanged).
type KeyOptions struct {
	// Semantic folds relational adjectives to nouns via adj_noun (false = lemma_key, true = semantic_key).
	Semantic bool
	// Profile selects a named tokenization/key preset: "" or "default" (legacy v0.2 behavior) | "technical".
	Profile string
	// StopWords overrides DefaultKeyStopWords when non-nil (empty map disables stop-word removal).
	StopWords map[string]struct{}
	// Tokenizer enables fine-grained tokenization flags when Profile == "technical" (or any flag is set).
	Tokenizer TokenizerOptions
	// PreservePrepBindings binds negation/range prepositions ("без", "не", "кроме", "против")
	// to their following lemma before sorting ("без сахара с молоком" -> "без_сахар молоко" != "без_молоко сахар").
	PreservePrepBindings bool
	// BindPurposePrep additionally binds purpose preposition "для" to its following noun
	// ("пластик для окон" -> "для_окно пластик" != "пластиковые окна" -> "окно пластик").
	BindPurposePrep bool
	// KeepWords prevents adj->noun folding for listed canonical lemmas (protects head nouns such as "пластик").
	KeepWords map[string]struct{}
	// ExcludeAdjToNoun disables adj->noun folding for specific adjective lemmas.
	ExcludeAdjToNoun map[string]struct{}
}

// TechToken represents a single token produced by TokenizeTechnical.
type TechToken struct {
	Text   string // normalized token text (lowercased, ё->е, decimal ','->'.', dim '×'->'x')
	Orig   string // original surface substring before lowercasing
	IsTech bool   // true for atomic technical tokens (decimals, dims, slash abbrs, plus tokens, contextual units)
	IsNum  bool   // true when token carries a numeric value (int, decimal, range, dim, num_slash)
}

var (
	cyrToLatHomoglyph = map[rune]rune{
		'а': 'a', 'А': 'A',
		'в': 'b', 'В': 'B',
		'с': 'c', 'С': 'C',
		'е': 'e', 'Е': 'E',
		'н': 'h', 'Н': 'H',
		'к': 'k', 'К': 'K',
		'м': 'm', 'М': 'M',
		'о': 'o', 'О': 'O',
		'р': 'p', 'Р': 'P',
		'т': 't', 'Т': 'T',
		'х': 'x', 'Х': 'X',
		'у': 'y', 'У': 'Y',
	}
	latToCyrHomoglyph = map[rune]rune{
		'a': 'а', 'A': 'А',
		'c': 'с', 'C': 'С',
		'e': 'е', 'E': 'Е',
		'k': 'к', 'K': 'К',
		'm': 'м', 'M': 'М',
		'o': 'о', 'O': 'О',
		'p': 'р', 'P': 'Р',
		't': 'т', 'T': 'Т',
		'x': 'х', 'X': 'Х',
		'y': 'у', 'Y': 'У',
		'h': 'н', 'H': 'Н',
		'b': 'в', 'B': 'В',
	}
)

func isCyrLetter(r rune) bool {
	return (r >= 'а' && r <= 'я') || (r >= 'А' && r <= 'Я') || r == 'ё' || r == 'Ё'
}

func isLatLetter(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}

// normalizeTokenHomoglyphs fixes mixed Cyrillic+Latin homoglyphs inside a single word token.
// Pure Cyrillic and pure Latin tokens are returned unchanged.
func normalizeTokenHomoglyphs(tok string) string {
	var hasCyr, hasLat bool
	var uniqCyr, uniqLat int
	var totalCyr, totalLat int
	for _, r := range tok {
		if isCyrLetter(r) {
			hasCyr = true
			totalCyr++
			if _, isHomo := cyrToLatHomoglyph[r]; !isHomo {
				uniqCyr++
			}
		} else if isLatLetter(r) {
			hasLat = true
			totalLat++
			if _, isHomo := latToCyrHomoglyph[r]; !isHomo {
				uniqLat++
			}
		}
	}
	if !hasCyr || !hasLat {
		return tok
	}

	// Decide dominant script: unique non-homoglyph letters win; tie-break by count (prefer Cyrillic for RU units like "cм").
	toLat := false
	if uniqLat > uniqCyr {
		toLat = true
	} else if uniqCyr > uniqLat {
		toLat = false
	} else {
		// All letters are homoglyphs (e.g. "cм" has lat 'c' + cyr 'м', "рrо" has uniqLat 'r' handled above)
		// Check known Russian 2-letter units/words first: "cм"/"см", "мaч", etc.
		toLat = totalLat > totalCyr
	}

	var b strings.Builder
	b.Grow(len(tok))
	for _, r := range tok {
		if toLat {
			if rep, ok := cyrToLatHomoglyph[r]; ok {
				b.WriteRune(rep)
				continue
			}
		} else {
			if rep, ok := latToCyrHomoglyph[r]; ok {
				b.WriteRune(rep)
				continue
			}
		}
		b.WriteRune(r)
	}
	return b.String()
}

// NormalizeHomoglyphs repairs mixed Cyrillic+Latin homoglyph characters inside individual tokens
// while preserving whitespace, punctuation, and pure-script tokens untouched.
// Examples: "cм" -> "см", "рrо" -> "pro", "intеl" -> "intel", "аsus" -> "asus", "sоny" -> "sony".
func NormalizeHomoglyphs(text string) string {
	if text == "" {
		return ""
	}
	var out strings.Builder
	out.Grow(len(text))
	var cur strings.Builder

	flush := func() {
		if cur.Len() == 0 {
			return
		}
		out.WriteString(normalizeTokenHomoglyphs(cur.String()))
		cur.Reset()
	}

	for _, r := range text {
		if isCyrLetter(r) || isLatLetter(r) || (r >= '0' && r <= '9') {
			cur.WriteRune(r)
		} else {
			flush()
			out.WriteRune(r)
		}
	}
	flush()
	return out.String()
}

// Precompiled technical token scanners (compiled once at init, 0 external deps).
var (
	// Direct match patterns (no lookbehind needed in Go RE2; boundaries handled by scanner loop).
	reSlashAbbr = regexp.MustCompile(`(?i)^(?:б\s*/\s*у|в\s*/\s*ч|ж\s*/\s*д|х\s*/\s*б|б\s*/\s*н)(?:$|[^a-zа-яё0-9])`)
	reDottedUni = regexp.MustCompile(`(?i)^(?:л\.\s*с\.?|кв\.\s*м\.?|куб\.\s*м\.?|[mм]\.2)(?:$|[^a-zа-яё0-9])`)
	reSlashUni  = regexp.MustCompile(`(?i)^(?:об/мин|км/ч|м/с|мг/мл|кг/см2?|л/мин|л/ч|вт/ч|квт/ч)(?:$|[^a-zа-яё0-9])`)
	reDim       = regexp.MustCompile(`(?i)^\d+(?:[.,]\d+)?(?:\s*[xх×*]\s*\d+(?:[.,]\d+)?)+`)
	reNumSlash  = regexp.MustCompile(`^\d+(?:[.,]\d+)?(?:/\d+(?:[.,]\d+)?)+`)
	reNumRange  = regexp.MustCompile(`^\d+(?:[.,]\d+)?(?:\s*[–—-]\s*\d+(?:[.,]\d+)?)+`)
	reDecimal   = regexp.MustCompile(`^\d+(?:[.,]\d+)+`)
	rePlusTok   = regexp.MustCompile(`(?i)^[a-zа-яё0-9]+\+{1,3}`)
	reSpace     = regexp.MustCompile(`\s+`)

	attachedUnits = map[string]struct{}{
		"мм": {}, "см": {}, "дм": {}, "м": {}, "км": {},
		"мг": {}, "г": {}, "гр": {}, "кг": {},
		"мл": {}, "л": {},
		"вт": {}, "квт": {}, "в": {}, "а": {}, "ма": {}, "ач": {}, "мач": {},
		"гц": {}, "кгц": {}, "мгц": {}, "ггц": {},
		"мб": {}, "гб": {}, "тб": {},
		"бар": {}, "атм": {}, "лм": {}, "втч": {}, "квтч": {},
		"mm": {}, "cm": {}, "kg": {}, "ml": {}, "gb": {}, "tb": {}, "mb": {}, "hz": {}, "mah": {},
	}

	unambiguousShortUnits = map[string]struct{}{
		"л": {}, "г": {}, "м": {}, "ч": {}, "v": {}, "w": {}, "l": {}, "g": {}, "m": {},
	}
	ambiguousShortUnits = map[string]struct{}{
		"в": {}, "а": {},
	}
	boundPrepositions = map[string]struct{}{
		"от": {}, "до": {},
	}
	unitFollowOK = map[string]struct{}{
		"и": {}, "или": {}, "а": {}, "с": {}, "со": {}, "для": {}, "на": {}, "по": {},
		"из": {}, "от": {}, "до": {}, "под": {}, "к": {}, "у": {}, "о": {}, "об": {},
		"без": {}, "не": {}, "купить": {}, "цена": {}, "цены": {}, "недорого": {}, "дешево": {},
		"фото": {}, "отзывы": {}, "отзыв": {}, "вольт": {}, "ампер": {}, "ватт": {},
		"гц": {}, "ма": {}, "ач": {}, "мач": {}, "аккумулятор": {}, "блок": {}, "питание": {},
		"зарядка": {}, "адаптер": {}, "двигатель": {}, "насос": {}, "лента": {}, "лампа": {},
	}

	negationBindPreps = map[string]struct{}{
		"без": {}, "не": {}, "кроме": {}, "против": {},
	}
)

func normalizeDottedUnit(s string) string {
	c := strings.TrimRight(reSpace.ReplaceAllString(s, ""), ".")
	switch c {
	case "л.с":
		return "л.с."
	case "кв.м":
		return "кв.м"
	case "куб.м":
		return "куб.м"
	case "m.2", "м.2":
		return "m.2"
	default:
		return c
	}
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func isAllAlpha(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !isCyrLetter(r) && !isLatLetter(r) {
			return false
		}
	}
	return true
}

func splitNumUnit(tok string) (num, unit string, ok bool) {
	// Match leading digits (optionally with decimal dot) followed by known unit letters.
	i := 0
	for i < len(tok) && ((tok[i] >= '0' && tok[i] <= '9') || tok[i] == '.') {
		i++
	}
	if i == 0 || i == len(tok) {
		return "", "", false
	}
	numPart := tok[:i]
	unitPart := tok[i:]
	if strings.HasPrefix(numPart, ".") || strings.HasSuffix(numPart, ".") {
		return "", "", false
	}
	if _, known := attachedUnits[unitPart]; known {
		return numPart, unitPart, true
	}
	return "", "", false
}

// isAlphaPrefixForCode reports whether s is a short alphanumeric model prefix like "p", "мб", "ecam", "wh", "rx", "rtx".
func isModelCodePrefix(s string) bool {
	if !isAllAlpha(s) {
		return false
	}
	n := utf8.RuneCountInString(s)
	if n == 0 || n > 5 {
		return false
	}
	// Never merge Russian common short words/prepositions with digits ("в 2025", "на 100", "до 50", "от 3", "по 2", "с 10", "за 5", "из 2").
	if _, stop := GeneralKeyStopWords[s]; stop {
		return false
	}
	if s == "без" || s == "не" || s == "за" || s == "при" || s == "над" || s == "про" || s == "раз" || s == "год" || s == "лет" || s == "шт" {
		return false
	}
	// Single Latin/Cyrillic model prefix (p-6000, мб-23) or Latin code prefix (ecam 220, wh-1000x)
	if !hasCyrillic(s) {
		return n >= 1 && n <= 5
	}
	// For Cyrillic: only 1-2 letter technical prefixes like "мб", "т", "к" (not regular words like "цена", "дом")
	return n <= 2
}

func startsWithDigit(s string) bool {
	return len(s) > 0 && s[0] >= '0' && s[0] <= '9'
}

// isUnitToken reports whether s is a measurement unit that may follow a number ("л", "кг", "мм", "вт", "шт", "дюйм", "в").
func isUnitToken(s string) bool {
	if _, ok := attachedUnits[s]; ok {
		return true
	}
	if _, ok := unambiguousShortUnits[s]; ok {
		return true
	}
	if _, ok := ambiguousShortUnits[s]; ok {
		return true
	}
	switch s {
	case "шт", "дюйм", "дюйма", "дюймов", "л.с.", "кв.м", "куб.м", "об/мин", "км/ч":
		return true
	}
	return false
}

type rawTechItem struct {
	norm      string
	orig      string
	kind      string // "tech", "tech_num", "word"
	isDecimal bool
}

// TokenizeTechnical splits text into tokens preserving technical e-commerce tokens:
// decimals ("2,6" -> "2.6"), dimensions ("100×100" -> "100x100"), slash markers ("б/у", "8/256"),
// dotted/slashed units ("л.с.", "m.2", "об/мин"), plus tokens ("18+", "c++", "pro+"),
// attached number+unit ("5мм" -> "5", "мм"), and contextual unit "В"/"А" after numbers
// ("12 В" / "3,3 В" preserved as unit, while "3 в 1" and "42 в москве" treat "в" as preposition).
func TokenizeTechnical(text string, opts TokenizerOptions) []TechToken {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	src := text
	if opts.NormalizeHomoglyphs {
		src = NormalizeHomoglyphs(src)
	}

	// Detect ALL-CAPS input so uppercase "В" is only treated as case-hint in mixed-case strings.
	var totalLetters, upperLetters int
	for _, r := range src {
		if unicode.IsLetter(r) {
			totalLetters++
			if unicode.IsUpper(r) {
				upperLetters++
			}
		}
	}
	isAllCaps := totalLetters > 3 && upperLetters == totalLetters

	var raw []rawTechItem
	n := len(src)
	i := 0
	for i < n {
		r, sz := utf8.DecodeRuneInString(src[i:])
		if !isWordRune(r) {
			i += sz
			continue
		}
		rest := src[i:]

		// 1. Slash abbreviations: б/у, в/ч, ж/д, х/б, б/н
		if loc := reSlashAbbr.FindStringIndex(rest); loc != nil {
			end := loc[1]
			// Trim trailing non-word boundary char captured by pattern
			sub := rest[:end]
			for len(sub) > 0 {
				lr, lsz := utf8.DecodeLastRuneInString(sub)
				if !isWordRune(lr) && lr != '/' {
					sub = sub[:len(sub)-lsz]
				} else {
					break
				}
			}
			low := normYo(strings.ToLower(sub))
			norm := strings.ReplaceAll(reSpace.ReplaceAllString(low, ""), " ", "")
			raw = append(raw, rawTechItem{norm: norm, orig: sub, kind: "tech"})
			i += len(sub)
			continue
		}

		// 2. Dotted units: л.с., кв.м, куб.м, m.2
		if loc := reDottedUni.FindStringIndex(rest); loc != nil {
			sub := rest[:loc[1]]
			for len(sub) > 0 {
				lr, lsz := utf8.DecodeLastRuneInString(sub)
				if !isWordRune(lr) && lr != '.' {
					sub = sub[:len(sub)-lsz]
				} else {
					break
				}
			}
			norm := normalizeDottedUnit(normYo(strings.ToLower(sub)))
			raw = append(raw, rawTechItem{norm: norm, orig: sub, kind: "tech"})
			i += len(sub)
			continue
		}

		// 3. Slashed units: об/мин, км/ч, м/с, мг/мл, кг/см2, л/мин, вт/ч, квт/ч
		if loc := reSlashUni.FindStringIndex(rest); loc != nil {
			sub := rest[:loc[1]]
			for len(sub) > 0 {
				lr, lsz := utf8.DecodeLastRuneInString(sub)
				if !isWordRune(lr) && lr != '/' {
					sub = sub[:len(sub)-lsz]
				} else {
					break
				}
			}
			norm := normYo(strings.ToLower(sub))
			raw = append(raw, rawTechItem{norm: norm, orig: sub, kind: "tech"})
			i += len(sub)
			continue
		}

		// Numeric-prefixed technical tokens
		if r >= '0' && r <= '9' {
			// 4. Dimensions: 100×100, 100 x 100, 500×500×500
			if loc := reDim.FindStringIndex(rest); loc != nil {
				sub := rest[:loc[1]]
				// Ensure we don't cut into a trailing letter of a word unless it's a known unit or boundary
				nextOK := len(sub) == len(rest)
				if !nextOK {
					nr, _ := utf8.DecodeRuneInString(rest[len(sub):])
					if !isWordRune(nr) {
						nextOK = true
					} else if opts.SplitNumberUnit {
						// Check if attached suffix is a unit (e.g. 100x100см)
						rem := rest[len(sub):]
						j := 0
						for j < len(rem) {
							rr, rsz := utf8.DecodeRuneInString(rem[j:])
							if !isWordRune(rr) {
								break
							}
							j += rsz
						}
						if _, ok := attachedUnits[normYo(strings.ToLower(rem[:j]))]; ok {
							nextOK = true
						}
					}
				}
				if nextOK {
					low := strings.ReplaceAll(strings.ToLower(sub), ",", ".")
					norm := strings.NewReplacer(" ", "", "х", "x", "×", "x", "*", "x").Replace(low)
					raw = append(raw, rawTechItem{norm: norm, orig: sub, kind: "tech_num", isDecimal: true})
					i += len(sub)
					continue
				}
			}

			// 5. Numeric slash: 8/256, 12/512, 205/55
			if loc := reNumSlash.FindStringIndex(rest); loc != nil {
				sub := rest[:loc[1]]
				if len(sub) == len(rest) || !isWordRune(firstRune(rest[len(sub):])) {
					norm := strings.ReplaceAll(sub, ",", ".")
					raw = append(raw, rawTechItem{norm: norm, orig: sub, kind: "tech_num", isDecimal: true})
					i += len(sub)
					continue
				}
			}

			// 6. Numeric range: 10–20, 10-20
			if loc := reNumRange.FindStringIndex(rest); loc != nil {
				sub := rest[:loc[1]]
				nextOK := len(sub) == len(rest)
				if !nextOK {
					nr := firstRune(rest[len(sub):])
					if !isWordRune(nr) {
						nextOK = true
					} else if opts.SplitNumberUnit {
						rem := rest[len(sub):]
						j := 0
						for j < len(rem) {
							rr, rsz := utf8.DecodeRuneInString(rem[j:])
							if !isWordRune(rr) {
								break
							}
							j += rsz
						}
						if _, ok := attachedUnits[normYo(strings.ToLower(rem[:j]))]; ok {
							nextOK = true
						}
					}
				}
				if nextOK {
					low := strings.ReplaceAll(sub, ",", ".")
					norm := strings.NewReplacer(" ", "", "–", "-", "—", "-").Replace(low)
					raw = append(raw, rawTechItem{norm: norm, orig: sub, kind: "tech_num", isDecimal: true})
					i += len(sub)
					continue
				}
			}

			// 7. Decimal: 2,6 -> 2.6, 0.5 -> 0.5, 3,3 -> 3.3
			if loc := reDecimal.FindStringIndex(rest); loc != nil {
				sub := rest[:loc[1]]
				nextOK := len(sub) == len(rest)
				if !nextOK {
					nr := firstRune(rest[len(sub):])
					if !isWordRune(nr) && nr != '.' && nr != ',' {
						nextOK = true
					} else if opts.SplitNumberUnit {
						rem := rest[len(sub):]
						j := 0
						for j < len(rem) {
							rr, rsz := utf8.DecodeRuneInString(rem[j:])
							if !isWordRune(rr) {
								break
							}
							j += rsz
						}
						if _, ok := attachedUnits[normYo(strings.ToLower(rem[:j]))]; ok {
							nextOK = true
						}
					}
				}
				if nextOK {
					norm := strings.ReplaceAll(sub, ",", ".")
					raw = append(raw, rawTechItem{norm: norm, orig: sub, kind: "tech_num", isDecimal: true})
					i += len(sub)
					continue
				}
			}
		}

		// 8. Plus-suffixed tokens: 18+, c++, pro+, s24+
		if loc := rePlusTok.FindStringIndex(rest); loc != nil {
			sub := rest[:loc[1]]
			if len(sub) == len(rest) || (!isWordRune(firstRune(rest[len(sub):])) && rest[len(sub)] != '+') {
				raw = append(raw, rawTechItem{norm: normYo(strings.ToLower(sub)), orig: sub, kind: "tech"})
				i += len(sub)
				continue
			}
		}

		// 9. Standard word / hyphenated token
		j := 0
		prevHyphen := false
		for j < len(rest) {
			rr, rsz := utf8.DecodeRuneInString(rest[j:])
			if isWordRune(rr) {
				j += rsz
				prevHyphen = false
			} else if rr == '-' && j > 0 && !prevHyphen {
				// Peek next rune: only include internal hyphen when followed by a word rune
				if j+rsz < len(rest) && isWordRune(firstRune(rest[j+rsz:])) {
					j += rsz
					prevHyphen = true
				} else {
					break
				}
			} else {
				break
			}
		}
		sub := strings.TrimRight(rest[:j], "-")
		if sub == "" {
			i += sz
			continue
		}
		low := normYo(strings.ToLower(sub))
		i += len(sub)

		// Optional SplitNumberUnit ("5мм" -> "5", "мм"; "12в" -> "12", "в")
		if opts.SplitNumberUnit && !strings.ContainsRune(low, '-') {
			if numPart, unitPart, ok := splitNumUnit(low); ok {
				raw = append(raw, rawTechItem{norm: numPart, orig: sub[:len(numPart)], kind: "tech_num", isDecimal: strings.ContainsRune(numPart, '.')})
				raw = append(raw, rawTechItem{norm: unitPart, orig: sub[len(numPart):], kind: "tech"})
				continue
			}
		}

		// Optional CompactCodes for hyphenated model codes ("p-6000" -> "p6000", "мб-23" -> "мб23")
		if opts.CompactCodes && strings.ContainsRune(low, '-') {
			parts := strings.Split(low, "-")
			if len(parts) == 2 && isModelCodePrefix(parts[0]) && startsWithDigit(parts[1]) {
				raw = append(raw, rawTechItem{norm: parts[0] + parts[1], orig: sub, kind: "tech"})
				continue
			}
		}

		// Optional SplitHyphens for alphabetic hyphenated words ("онлайн-курсы" -> "онлайн", "курсы")
		if opts.SplitHyphens && strings.ContainsRune(low, '-') {
			parts := strings.Split(low, "-")
			origParts := strings.Split(sub, "-")
			allAlpha := true
			for _, p := range parts {
				if !isAllAlpha(p) {
					allAlpha = false
					break
				}
			}
			if allAlpha {
				for idx, p := range parts {
					if p == "" {
						continue
					}
					op := p
					if idx < len(origParts) {
						op = origParts[idx]
					}
					raw = append(raw, rawTechItem{norm: p, orig: op, kind: "word"})
				}
				continue
			}
		}

		if isAllDigits(low) {
			raw = append(raw, rawTechItem{norm: low, orig: sub, kind: "tech_num"})
		} else {
			raw = append(raw, rawTechItem{norm: low, orig: sub, kind: "word"})
		}
	}

	// Second pass: optional CompactCodes for space-separated model codes ("p 6000" -> "p6000", "мб 23" -> "мб23", "ecam 220" -> "ecam220").
	// Never merge when the number is followed by a unit ("qrevo 5 л" keeps "5 л" as a volume, not a model code "qrevo5").
	if opts.CompactCodes && len(raw) >= 2 {
		merged := make([]rawTechItem, 0, len(raw))
		for k := 0; k < len(raw); k++ {
			if k+1 < len(raw) && raw[k].kind == "word" && isModelCodePrefix(raw[k].norm) &&
				(raw[k+1].kind == "tech_num" || (raw[k+1].kind == "word" && startsWithDigit(raw[k+1].norm))) &&
				!raw[k+1].isDecimal && !(k+2 < len(raw) && isUnitToken(raw[k+2].norm)) {
				merged = append(merged, rawTechItem{
					norm: raw[k].norm + raw[k+1].norm,
					orig: raw[k].orig + " " + raw[k+1].orig,
					kind: "tech",
				})
				k++
				continue
			}
			merged = append(merged, raw[k])
		}
		raw = merged
	}

	// Third pass: contextual unit classification ("12 В" / "3,3 В" vs "3 в 1" vs "42 в москве")
	out := make([]TechToken, 0, len(raw))
	for idx, it := range raw {
		if it.kind == "tech" || it.kind == "tech_num" {
			out = append(out, TechToken{Text: it.norm, Orig: it.orig, IsTech: true, IsNum: it.kind == "tech_num"})
			continue
		}

		var prev, nxt *rawTechItem
		if idx > 0 {
			prev = &raw[idx-1]
		}
		if idx+1 < len(raw) {
			nxt = &raw[idx+1]
		}

		// Range boundary preposition before number ("до 50 см" != "от 50 см" != "50 см")
		if _, isBound := boundPrepositions[it.norm]; isBound && nxt != nil && nxt.kind == "tech_num" {
			out = append(out, TechToken{Text: it.norm, Orig: it.orig, IsTech: true})
			continue
		}

		// Unambiguous single-letter units after number ("5 л", "100 г", "2 м")
		if _, isU := unambiguousShortUnits[it.norm]; isU && prev != nil && prev.kind == "tech_num" {
			out = append(out, TechToken{Text: it.norm, Orig: it.orig, IsTech: true})
			continue
		}

		// Ambiguous single-letter units "в"/"а" after number:
		// "3 в 1" / "2 в 1" -> preposition (nxt is tech_num!)
		// "12 В", "3,3 В", "12 в аккумулятор", "220 в" (at end) -> unit (IsTech=true!)
		// "ботинки 42 в москве" -> preposition (lowercase, integer prev, followed by non-unit word)
		if _, isAmb := ambiguousShortUnits[it.norm]; isAmb && prev != nil && prev.kind == "tech_num" {
			isPrepositionPattern := nxt != nil && nxt.kind == "tech_num"
			isOrigUpper := !isAllCaps && it.orig != strings.ToLower(it.orig)
			isUnit := !isPrepositionPattern && (isOrigUpper ||
				prev.isDecimal ||
				nxt == nil ||
				nxt.kind == "tech" ||
				func() bool { _, ok := unitFollowOK[nxt.norm]; return ok }())
			out = append(out, TechToken{Text: it.norm, Orig: it.orig, IsTech: isUnit})
			continue
		}

		// Single uppercase letter class/vitamin designation not at position 0 ("Витамин С", "Класс А")
		if utf8.RuneCountInString(it.norm) == 1 && isAllAlpha(it.norm) && !isAllCaps && it.orig != strings.ToLower(it.orig) && idx > 0 {
			out = append(out, TechToken{Text: it.norm, Orig: it.orig, IsTech: true})
			continue
		}

		out = append(out, TechToken{Text: it.norm, Orig: it.orig, IsTech: false})
	}
	return out
}

func firstRune(s string) rune {
	r, _ := utf8.DecodeRuneInString(s)
	return r
}

// KeyWithOptions builds a canonical sorted, deduplicated lemma or semantic key using opt-in v0.3 profiles.
// When opts.Profile == "" or "default" and no v0.3 flags are enabled, it delegates directly to
// LemmaKeyWithStop / SemanticKeyWithStop for 100% backward compatibility with v0.2 keys.
// LemmatizeAllWithOptions returns (lemmatized_text, lemma_key, semantic_key) using opt-in v0.3 key options
// (technical profile, preposition bindings, keep_words, compact_codes...). The lemmatized text column is
// identical to LemmatizeString. With zero-value options it is equivalent to LemmatizeAllWithStop.
func (l *Lemmatizer) LemmatizeAllWithOptions(text string, opts KeyOptions) (lemText, lemmaKey, semanticKey string) {
	if !keyOptionsActive(opts) {
		stop := opts.StopWords
		if stop == nil {
			stop = DefaultKeyStopWords
		}
		return l.LemmatizeAllWithStop(text, stop)
	}
	o1 := opts
	o1.Semantic = false
	o2 := opts
	o2.Semantic = true
	return l.LemmatizeString(text), l.KeyWithOptions(text, o1), l.KeyWithOptions(text, o2)
}

func keyOptionsActive(opts KeyOptions) bool {
	return strings.EqualFold(opts.Profile, "technical") ||
		opts.Tokenizer.NormalizeHomoglyphs ||
		opts.Tokenizer.SplitHyphens ||
		opts.Tokenizer.SplitNumberUnit ||
		opts.Tokenizer.CompactCodes ||
		opts.PreservePrepBindings || opts.BindPurposePrep ||
		len(opts.KeepWords) > 0 || len(opts.ExcludeAdjToNoun) > 0
}

func (l *Lemmatizer) KeyWithOptions(text string, opts KeyOptions) string {
	stop := opts.StopWords
	if stop == nil {
		stop = DefaultKeyStopWords
	}

	useTech := strings.EqualFold(opts.Profile, "technical") ||
		opts.Tokenizer.NormalizeHomoglyphs ||
		opts.Tokenizer.SplitHyphens ||
		opts.Tokenizer.SplitNumberUnit ||
		opts.Tokenizer.CompactCodes
	hasKeyMods := opts.PreservePrepBindings || opts.BindPurposePrep ||
		len(opts.KeepWords) > 0 || len(opts.ExcludeAdjToNoun) > 0

	if !useTech && !hasKeyMods {
		if opts.Semantic {
			return l.SemanticKeyWithStop(text, stop)
		}
		return l.LemmaKeyWithStop(text, stop)
	}

	tokOpts := opts.Tokenizer
	if strings.EqualFold(opts.Profile, "technical") {
		if !opts.Tokenizer.NormalizeHomoglyphs {
			tokOpts.NormalizeHomoglyphs = true
		}
		if !opts.Tokenizer.SplitNumberUnit {
			tokOpts.SplitNumberUnit = true
		}
	}

	var toks []TechToken
	if useTech {
		toks = TokenizeTechnical(text, tokOpts)
	} else {
		plain := Tokenize(text)
		toks = make([]TechToken, len(plain))
		for i, t := range plain {
			toks[i] = TechToken{Text: t, Orig: t}
		}
	}
	if len(toks) == 0 {
		return ""
	}

	// Build canonical terms with optional preposition bindings & head/adj guards.
	seen := make(map[string]struct{}, len(toks))
	keys := make([]string, 0, len(toks))
	pendingPrep := ""

	addKey := func(k string) {
		if k == "" {
			return
		}
		if _, dup := seen[k]; !dup {
			seen[k] = struct{}{}
			keys = append(keys, k)
		}
	}

	for _, tk := range toks {
		t := tk.Text
		if t == "" {
			continue
		}

		// Technical atomic tokens (decimals, dims, б/у, 12 В, m.2, c++, 18+) are NEVER dropped as stop words
		// and never distorted by lemmatization or adj_to_noun.
		if tk.IsTech {
			if pendingPrep != "" {
				addKey(pendingPrep + "_" + t)
				pendingPrep = ""
			} else {
				addKey(t)
			}
			continue
		}

		// Check if token starts a bound preposition phrase ("без сахара", "для окон")
		if opts.PreservePrepBindings {
			if _, isNeg := negationBindPreps[t]; isNeg {
				if pendingPrep != "" {
					addKey(pendingPrep)
				}
				pendingPrep = t
				continue
			}
		}
		if opts.BindPurposePrep && t == "для" {
			if pendingPrep != "" {
				addKey(pendingPrep)
			}
			pendingPrep = "для"
			continue
		}

		// KeepWords always win over stop words (custom or default): "класс а" with keep_words={"а"} -> "а класс".
		_, keepRaw := opts.KeepWords[t]
		if !keepRaw {
			if tn := normYo(t); tn != t {
				_, keepRaw = opts.KeepWords[tn]
			}
		}

		// Stop-word check (raw form)
		if !keepRaw {
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
		if _, keepLm := opts.KeepWords[lm]; !keepRaw && !keepLm {
			if _, skip := stop[lm]; skip {
				continue
			}
		}

		// Optional relational adj -> noun folding with KeepWords / ExcludeAdjToNoun guards
		if opts.Semantic {
			skipFold := false
			if _, ex := opts.ExcludeAdjToNoun[lm]; ex {
				skipFold = true
			}
			if !skipFold {
				if noun, ok := l.lookupAdjToNoun(lm); ok {
					// If target noun OR source lemma is protected in KeepWords, do not collapse head/modifier
					if _, keepSrc := opts.KeepWords[lm]; keepSrc {
						skipFold = true
					} else if _, keepDst := opts.KeepWords[noun]; keepDst {
						skipFold = true
					}
					if !skipFold {
						lm = noun
					}
				}
			}
		}

		if pendingPrep != "" {
			addKey(pendingPrep + "_" + lm)
			pendingPrep = ""
		} else {
			addKey(lm)
		}
	}

	if pendingPrep != "" {
		addKey(pendingPrep)
	}

	sort.Strings(keys)
	return strings.Join(keys, " ")
}
