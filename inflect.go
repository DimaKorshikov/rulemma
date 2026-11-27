package rulemma

import (
	"bufio"
	"bytes"
	"fmt"
	"io/fs"
	"math"
	"strconv"
	"strings"
	"unicode"
)

// Paradigm word flags (must match tools/build_dicts.py).
const (
	pfKindMask uint16 = 0x3
	pfKindNoun uint16 = 1
	pfKindAdj  uint16 = 2

	pfGenderShift        = 2
	pfGenderMask  uint16 = 0x3 << pfGenderShift

	pfAnim uint16 = 1 << 4
	pfInan uint16 = 1 << 5
	pfPltm uint16 = 1 << 6
	pfSgtm uint16 = 1 << 7
	pfFixd uint16 = 1 << 8
)

type paradigmDef struct {
	kind    byte // 'N' (12 slots) or 'A' (26 slots)
	strip   uint8
	endings []string
}

type paradigmRef struct {
	pid   uint16
	flags uint16
}

type paradigmStore struct {
	defs  []paradigmDef
	words *compactU32Map
}

func packParadigmRef(pid, flags uint16) uint32 {
	return uint32(pid) | (uint32(flags) << 16)
}

func unpackParadigmRef(v uint32) paradigmRef {
	return paradigmRef{
		pid:   uint16(v & 0xFFFF),
		flags: uint16(v >> 16),
	}
}

func (ps *paradigmStore) getWord(lemma string) (paradigmRef, bool) {
	if ps == nil || ps.words == nil {
		return paradigmRef{}, false
	}
	v, ok := ps.words.Get(lemma)
	if !ok {
		return paradigmRef{}, false
	}
	return unpackParadigmRef(v), true
}

func (l *Lemmatizer) getParadigmStore() *paradigmStore {
	l.ensureParadigms()
	return l.paradigmStore.Load()
}

func loadParadigmsFS(fsys fs.FS) ([]paradigmDef, *compactU32Map, error) {
	name := firstExistingFS(fsys, "paradigms.tsv.gz", "paradigms.tsv")
	if name == "" {
		return nil, nil, nil
	}
	rc, err := openMaybeGzipFS(fsys, name)
	if err != nil {
		return nil, nil, fmt.Errorf("open %s: %w", name, err)
	}
	defer rc.Close()

	// Index 0 is reserved for Fixd / indeclinable words.
	defs := make([]paradigmDef, 1, 400)
	words := newCompactU32Map(45000, 750*1024)

	sc := bufio.NewScanner(rc)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		line := trimCR(sc.Bytes())
		if len(line) < 3 || line[0] == '#' {
			continue
		}
		switch line[0] {
		case 'P':
			// P\t<pid>\t<kind>\t<strip>\t<endings_csv>
			parts := strings.SplitN(string(line), "\t", 5)
			if len(parts) != 5 {
				continue
			}
			pid, err1 := strconv.Atoi(parts[1])
			strip, err2 := strconv.Atoi(parts[3])
			if err1 != nil || err2 != nil || pid <= 0 || len(parts[2]) == 0 {
				continue
			}
			if pid >= len(defs) {
				grown := make([]paradigmDef, pid+1)
				copy(grown, defs)
				defs = grown
			}
			defs[pid] = paradigmDef{
				kind:    parts[2][0],
				strip:   uint8(strip),
				endings: strings.Split(parts[4], ","),
			}
		case 'W':
			// W\t<lemma>\t<pid>\t<flags_hex>
			if line[1] != '\t' {
				continue
			}
			rest := line[2:]
			t1 := bytes.IndexByte(rest, '\t')
			if t1 <= 0 || t1+1 >= len(rest) {
				continue
			}
			lemma := rest[:t1]
			rest2 := rest[t1+1:]
			t2 := bytes.IndexByte(rest2, '\t')
			if t2 <= 0 || t2+1 >= len(rest2) {
				continue
			}
			pid, ok1 := parseDecUint16Bytes(rest2[:t2])
			fl32, ok2 := parseHexUint32Bytes(rest2[t2+1:])
			if !ok1 || !ok2 || fl32 > 0xFFFF {
				continue
			}
			words.putBytes(lemma, packParadigmRef(pid, uint16(fl32)))
		}
	}
	if err := sc.Err(); err != nil {
		return nil, nil, fmt.Errorf("scan %s: %w", name, err)
	}
	words.freeze()
	return defs, words, nil
}

func parseCaseIndex(c string) (int, bool) {
	switch strings.ToLower(strings.TrimSpace(c)) {
	case "nomn", "nom", "и", "им", "именительный":
		return 0, true
	case "gent", "gen", "gen2", "р", "род", "родительный":
		return 1, true
	case "datv", "dat", "д", "дат", "дательный":
		return 2, true
	case "accs", "acc", "в", "вин", "винительный":
		return 3, true
	case "ablt", "abl", "ins", "inst", "т", "тв", "творительный":
		return 4, true
	case "loct", "loc", "loc2", "pre", "prep", "п", "пр", "предложный":
		return 5, true
	default:
		return -1, false
	}
}

func parseNumberNorm(n string) string {
	switch strings.ToLower(strings.TrimSpace(n)) {
	case "sing", "sg", "ед":
		return "sing"
	case "plur", "pl", "мн":
		return "plur"
	default:
		return ""
	}
}

func parseGenderNorm(g string) string {
	switch strings.ToLower(strings.TrimSpace(g)) {
	case "masc", "m", "м", "муж":
		return "masc"
	case "femn", "f", "ж", "жен":
		return "femn"
	case "neut", "n", "с", "ср", "сред":
		return "neut"
	default:
		return ""
	}
}

func applyParadigmSlot(lemma string, def paradigmDef, slot int) (string, bool) {
	if slot < 0 || slot >= len(def.endings) {
		return "", false
	}
	ending := def.endings[slot]
	if ending == "-" {
		return "", false
	}
	runes := []rune(lemma)
	strip := int(def.strip)
	if strip > len(runes) {
		return "", false
	}
	return string(runes[:len(runes)-strip]) + ending, true
}

func matchCasing(orig, inflected string) string {
	if orig == "" || inflected == "" {
		return inflected
	}
	origRunes := []rune(orig)
	allUpper := true
	hasLetter := false
	for _, r := range origRunes {
		if unicode.IsLetter(r) {
			hasLetter = true
			if !unicode.IsUpper(r) {
				allUpper = false
				break
			}
		}
	}
	if hasLetter && allUpper && len(origRunes) > 1 {
		return strings.ToUpper(inflected)
	}
	if unicode.IsUpper(origRunes[0]) {
		infRunes := []rune(inflected)
		infRunes[0] = unicode.ToUpper(infRunes[0])
		return string(infRunes)
	}
	return inflected
}

func isPureCyrillicWord(s string) bool {
	if s == "" {
		return false
	}
	hasCyr := false
	for _, r := range s {
		if isCyrLetter(r) {
			hasCyr = true
		} else if r == '-' {
			continue
		} else {
			return false
		}
	}
	return hasCyr
}

// Inflect inflects a single Russian noun or adjective/participle to targetCase
// ("nomn", "gent", "datv", "accs", "ablt", "loct") and optional targetNumber ("sing", "plur", or "" to preserve).
// Indeclinable (Fixd), pluralia tantum (Pltm), singularia tantum (Sgtm), Latin tokens, and SKU codes are protected.
func (l *Lemmatizer) Inflect(word, targetCase, targetNumber string) (string, bool) {
	trimmed := strings.TrimSpace(word)
	if trimmed == "" || !isPureCyrillicWord(trimmed) {
		return trimmed, false
	}
	ps := l.getParadigmStore()
	if ps == nil || ps.words.Len() == 0 {
		return trimmed, false
	}
	caseIdx, ok := parseCaseIndex(targetCase)
	if !ok {
		return trimmed, false
	}
	numWant := parseNumberNorm(targetNumber)
	low := normYo(strings.ToLower(trimmed))
	lm := l.Word(low)

	ref, found := ps.getWord(lm)
	if !found {
		return trimmed, false
	}
	if ref.flags&pfFixd != 0 {
		return trimmed, true
	}
	if int(ref.pid) >= len(ps.defs) || ref.pid == 0 {
		return trimmed, false
	}
	def := ps.defs[ref.pid]

	if def.kind == 'N' {
		effNum := numWant
		if ref.flags&pfPltm != 0 {
			effNum = "plur"
		} else if effNum == "" {
			// Preserve input word's number when unspecified
			if bits, has := l.morphBits(low); has {
				nCode := int((bits & morphNumberMask) >> morphNumberShift)
				if nCode == 2 { // plur
					effNum = "plur"
				} else {
					effNum = "sing"
				}
			} else {
				effNum = "sing"
			}
		}

		slot := caseIdx
		if effNum == "plur" {
			slot = 6 + caseIdx
		}
		res, ok := applyParadigmSlot(lm, def, slot)
		if !ok && effNum == "plur" && ref.flags&pfSgtm != 0 {
			// Guard Sgtm: fall back to singular case form
			res, ok = applyParadigmSlot(lm, def, caseIdx)
		}
		if !ok {
			return trimmed, false
		}
		return matchCasing(trimmed, res), true
	}

	if def.kind == 'A' {
		// Detect input gender/number when unspecified
		gender := "masc"
		effNum := numWant
		if bits, has := l.morphBits(low); has {
			gIdx := int((bits & morphGenderMask) >> morphGenderShift)
			if gIdx > 0 && gIdx < len(genderNames) {
				gender = genderNames[gIdx]
			}
			if effNum == "" {
				nIdx := int((bits & morphNumberMask) >> morphNumberShift)
				if nIdx == 2 {
					effNum = "plur"
				} else {
					effNum = "sing"
				}
			}
		}
		if effNum == "" {
			effNum = "sing"
		}
		return l.InflectAdj(trimmed, targetCase, gender, effNum, false)
	}
	return trimmed, false
}

// InflectAdj inflects a Russian adjective or full participle to the specified case, gender, number, and animacy.
func (l *Lemmatizer) InflectAdj(adj, targetCase, gender, number string, animate bool) (string, bool) {
	trimmed := strings.TrimSpace(adj)
	if trimmed == "" || !isPureCyrillicWord(trimmed) {
		return trimmed, false
	}
	ps := l.getParadigmStore()
	if ps == nil || ps.words.Len() == 0 {
		return trimmed, false
	}
	caseIdx, ok := parseCaseIndex(targetCase)
	if !ok {
		return trimmed, false
	}
	low := normYo(strings.ToLower(trimmed))
	lm := l.Word(low)

	ref, found := ps.getWord(lm)
	if !found || ref.flags&pfKindMask != pfKindAdj {
		return trimmed, false
	}
	if ref.flags&pfFixd != 0 {
		return trimmed, true
	}
	if int(ref.pid) >= len(ps.defs) || ref.pid == 0 {
		return trimmed, false
	}
	def := ps.defs[ref.pid]
	if def.kind != 'A' {
		return trimmed, false
	}

	num := parseNumberNorm(number)
	if num == "" {
		num = "sing"
	}
	gen := parseGenderNorm(gender)
	if gen == "" {
		gen = "masc"
	}

	var slot int
	if num == "plur" {
		if caseIdx == 3 && animate && len(def.endings) > 25 {
			slot = 25
		} else {
			slot = 18 + caseIdx
		}
	} else {
		switch gen {
		case "femn":
			slot = 6 + caseIdx
		case "neut":
			slot = 12 + caseIdx
		default: // masc
			if caseIdx == 3 && animate && len(def.endings) > 24 {
				slot = 24
			} else {
				slot = caseIdx
			}
		}
	}

	res, ok := applyParadigmSlot(lm, def, slot)
	if !ok {
		return trimmed, false
	}
	return matchCasing(trimmed, res), true
}

// phraseTailStopPreps marks prepositions that begin an uninflected modifier tail
// (e.g. "чехол для телефона" -> "чехла для телефона", "крем от морщин" -> "крема от морщин").
var phraseTailStopPreps = map[string]struct{}{
	"для": {}, "в": {}, "во": {}, "на": {}, "с": {}, "со": {}, "из": {}, "от": {},
	"до": {}, "по": {}, "без": {}, "к": {}, "ко": {}, "о": {}, "об": {}, "при": {},
	"под": {}, "над": {}, "за": {}, "у": {}, "через": {}, "между": {}, "про": {},
	"против": {}, "после": {}, "около": {},
}

// InflectPhrase inflects a short Russian noun phrase ([Adj/Part*] + Noun [+ brand/SKU / prep tail])
// into targetCase and optional targetNumber ("sing", "plur", or "" to preserve head noun's number).
// Brands, Latin words, numeric SKU codes, prepositional tails, and dependent genitive nouns stay intact.
//
// Examples:
//   - InflectPhrase("стиральная машина LG F2V5", "accs", "") -> "стиральную машину LG F2V5"
//   - InflectPhrase("Краснодарский край", "loct", "")        -> "Краснодарском крае"
//   - InflectPhrase("зимние кроссовки Nike Air", "gent", "") -> "зимних кроссовок Nike Air"
//   - InflectPhrase("ножницы портновские", "datv", "")       -> "ножницам портновским"
//   - InflectPhrase("чехол для телефона", "gent", "")        -> "чехла для телефона"
func (l *Lemmatizer) InflectPhrase(phrase, targetCase, targetNumber string) (string, bool) {
	fields := strings.Fields(phrase)
	if len(fields) == 0 {
		return "", false
	}
	if len(fields) == 1 {
		return l.Inflect(fields[0], targetCase, targetNumber)
	}
	if _, ok := parseCaseIndex(targetCase); !ok {
		return phrase, false
	}
	ps := l.getParadigmStore()
	if ps == nil || ps.words.Len() == 0 {
		return phrase, false
	}

	// Determine the active noun-phrase span before any preposition tail or Latin/numeric SKU tail.
	type tokMeta struct {
		orig  string
		low   string
		lemma string
		ref   paradigmRef
		hasP  bool
		isAdj bool
		isN   bool
	}

	metas := make([]tokMeta, len(fields))
	headIdx := -1
	activeEnd := len(fields)

	for i, f := range fields {
		low := normYo(strings.ToLower(f))
		if i > 0 {
			if _, isPrep := phraseTailStopPreps[low]; isPrep {
				activeEnd = i
				break
			}
		}
		if !isPureCyrillicWord(f) {
			if headIdx >= 0 {
				activeEnd = i
				break
			}
			// Leading non-Cyrillic brand before noun (e.g. "Nike зимние кроссовки")
			metas[i] = tokMeta{orig: f, low: low}
			continue
		}
		lm := l.Word(low)
		ref, hasP := ps.getWord(lm)
		m := tokMeta{
			orig:  f,
			low:   low,
			lemma: lm,
			ref:   ref,
			hasP:  hasP,
			isAdj: hasP && (ref.flags&pfKindMask == pfKindAdj),
			isN:   hasP && (ref.flags&pfKindMask == pfKindNoun),
		}
		metas[i] = m

		if headIdx < 0 && m.isN {
			headIdx = i
		} else if headIdx >= 0 && m.isN {
			// A second noun after head noun (e.g. "фильтр воды", "ремонт телефонов") starts dependent tail.
			activeEnd = i
			break
		}
	}

	if headIdx < 0 {
		// No head noun found: try inflecting pure Cyrillic adjectives/words before tail.
		changed := false
		out := make([]string, len(fields))
		copy(out, fields)
		for i := 0; i < activeEnd; i++ {
			if metas[i].hasP {
				if inf, ok := l.Inflect(fields[i], targetCase, targetNumber); ok {
					out[i] = inf
					changed = true
				}
			}
		}
		return strings.Join(out, " "), changed
	}

	head := metas[headIdx]
	// Determine effective number, gender, and animacy from head noun.
	effNum := parseNumberNorm(targetNumber)
	if head.ref.flags&pfPltm != 0 {
		effNum = "plur"
	} else if effNum == "" {
		if bits, has := l.morphBits(head.low); has && int((bits&morphNumberMask)>>morphNumberShift) == 2 {
			effNum = "plur"
		} else {
			effNum = "sing"
		}
	}
	if head.ref.flags&pfSgtm != 0 && effNum == "plur" {
		effNum = "sing"
	}

	gIdx := int((head.ref.flags & pfGenderMask) >> pfGenderShift)
	headGender := ""
	if gIdx > 0 && gIdx < len(genderNames) {
		headGender = genderNames[gIdx]
	}
	headAnim := head.ref.flags&pfAnim != 0

	out := make([]string, len(fields))
	copy(out, fields)
	anyOK := false

	if infHead, ok := l.Inflect(head.orig, targetCase, effNum); ok {
		out[headIdx] = infHead
		anyOK = true
	}

	for i := 0; i < activeEnd; i++ {
		if i == headIdx {
			continue
		}
		if metas[i].isAdj {
			if infAdj, ok := l.InflectAdj(metas[i].orig, targetCase, headGender, effNum, headAnim); ok {
				out[i] = infAdj
				anyOK = true
			}
		}
	}

	return strings.Join(out, " "), anyOK
}

// AllForms returns all deduplicated inflected wordforms from the paradigm of word (in canonical order).
func (l *Lemmatizer) AllForms(word string) []string {
	trimmed := strings.TrimSpace(word)
	if trimmed == "" || !isPureCyrillicWord(trimmed) {
		return nil
	}
	ps := l.getParadigmStore()
	if ps == nil || ps.words.Len() == 0 {
		return nil
	}
	low := normYo(strings.ToLower(trimmed))
	lm := l.Word(low)
	ref, found := ps.getWord(lm)
	if !found {
		return []string{lm}
	}
	if ref.flags&pfFixd != 0 || ref.pid == 0 || int(ref.pid) >= len(ps.defs) {
		return []string{lm}
	}
	def := ps.defs[ref.pid]
	seen := make(map[string]struct{}, len(def.endings))
	out := make([]string, 0, len(def.endings))
	for slot := range def.endings {
		form, ok := applyParadigmSlot(lm, def, slot)
		if !ok || form == "" {
			continue
		}
		if _, dup := seen[form]; !dup {
			seen[form] = struct{}{}
			out = append(out, form)
		}
	}
	return out
}

// PluralizeForm selects the Russian grammatical form matching numeric quantity n:
//   - fractional numbers (e.g. 1.5, 0.5) -> two (genitive singular: "1.5 товара")
//   - n % 100 in 11..14 -> five ("11 товаров")
//   - n % 10 == 1 -> one ("21 товар")
//   - n % 10 in 2..4 -> two ("2 товара")
//   - otherwise -> five ("5 товаров")
func PluralizeForm(n float64, one, two, five string) string {
	abs := math.Abs(n)
	if math.Abs(abs-math.Trunc(abs)) > 1e-9 {
		return two
	}
	iv := int64(math.Round(abs))
	mod100 := iv % 100
	if mod100 >= 11 && mod100 <= 14 {
		return five
	}
	switch iv % 10 {
	case 1:
		return one
	case 2, 3, 4:
		return two
	default:
		return five
	}
}

func formatAgreementNumber(n float64) string {
	if math.Abs(n-math.Trunc(n)) <= 1e-9 {
		return strconv.FormatInt(int64(math.Round(n)), 10)
	}
	return strconv.FormatFloat(n, 'f', -1, 64)
}

// AgreeNumber formats a Russian quantity + agreed noun (e.g. "21 товар", "2 товара", "5 товаров",
// "1.5 товара", "5 ножниц"). Handles Pltm, Sgtm, and Fixd nouns safely.
func (l *Lemmatizer) AgreeNumber(n float64, word string) string {
	numStr := formatAgreementNumber(n)
	trimmed := strings.TrimSpace(word)
	if trimmed == "" {
		return numStr
	}

	low := normYo(strings.ToLower(trimmed))
	lm := l.Word(low)
	ps := l.getParadigmStore()
	ref, found := ps.getWord(lm)
	if !found || ref.flags&pfKindMask != pfKindNoun || ref.flags&pfFixd != 0 {
		return numStr + " " + trimmed
	}

	var one, two, five string
	if ref.flags&pfPltm != 0 {
		// Pluralia tantum (ножницы, духи, сутки): 1/21 ножницы, 2..4/5 ножниц
		one, _ = l.Inflect(trimmed, "nomn", "plur")
		two, _ = l.Inflect(trimmed, "gent", "plur")
		five = two
	} else if ref.flags&pfSgtm != 0 {
		one, _ = l.Inflect(trimmed, "nomn", "sing")
		two, _ = l.Inflect(trimmed, "gent", "sing")
		five = two
	} else {
		one, _ = l.Inflect(trimmed, "nomn", "sing")
		two, _ = l.Inflect(trimmed, "gent", "sing")
		five, _ = l.Inflect(trimmed, "gent", "plur")
	}
	if one == "" {
		one = trimmed
	}
	if two == "" {
		two = one
	}
	if five == "" {
		five = two
	}
	return numStr + " " + PluralizeForm(n, one, two, five)
}

func isRusConsonant(r rune) bool {
	switch r {
	case 'б', 'в', 'г', 'д', 'ж', 'з', 'й', 'к', 'л', 'м', 'н', 'п', 'р', 'с', 'т', 'ф', 'х', 'ц', 'ч', 'ш', 'щ':
		return true
	}
	return false
}

// Preposition selects the natural Russian phonetic allomorph of a preposition ("в/во", "с/со", "к/ко", "о/об/обо")
// for the given nextWord, preserving capitalization of prep.
func Preposition(prep, nextWord string) string {
	p := strings.TrimSpace(prep)
	nw := normYo(strings.ToLower(strings.TrimSpace(nextWord)))
	if p == "" || nw == "" {
		return p
	}
	pLow := normYo(strings.ToLower(p))
	rNext := []rune(nw)
	if len(rNext) == 0 {
		return p
	}
	r0 := rNext[0]
	var r1 rune
	if len(rNext) > 1 {
		r1 = rNext[1]
	}

	chosen := pLow
	switch pLow {
	case "в", "во":
		chosen = "в"
		if ((r0 == 'в' || r0 == 'ф') && isRusConsonant(r1)) ||
			strings.HasPrefix(nw, "вс") || strings.HasPrefix(nw, "мног") ||
			nw == "сне" || nw == "дворе" || nw == "льду" || nw == "рту" ||
			nw == "время" || nw == "лбу" || nw == "мгле" || nw == "мрак" || nw == "мраке" {
			chosen = "во"
		}
	case "с", "со":
		chosen = "с"
		if ((r0 == 'с' || r0 == 'з' || r0 == 'ш' || r0 == 'ж' || r0 == 'щ') && isRusConsonant(r1)) ||
			strings.HasPrefix(nw, "вс") || strings.HasPrefix(nw, "мн") ||
			strings.HasPrefix(nw, "дн") || strings.HasPrefix(nw, "льн") ||
			strings.HasPrefix(nw, "лб") || strings.HasPrefix(nw, "рт") ||
			strings.HasPrefix(nw, "рж") || nw == "сна" || nw == "сном" {
			chosen = "со"
		}
	case "к", "ко":
		chosen = "к"
		if (r0 == 'к' && isRusConsonant(r1)) ||
			strings.HasPrefix(nw, "вс") || nw == "мне" ||
			nw == "дню" || nw == "дну" || nw == "сну" || nw == "лбу" || nw == "рту" {
			chosen = "ко"
		}
	case "о", "об", "обо":
		chosen = "о"
		if nw == "мне" || nw == "всем" || nw == "всех" || nw == "всей" || nw == "всему" || nw == "всего" {
			chosen = "обо"
		} else if r0 == 'а' || r0 == 'и' || r0 == 'о' || r0 == 'у' || r0 == 'э' {
			chosen = "об"
		}
	}
	return matchCasing(p, chosen)
}
