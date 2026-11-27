package rulemma

import (
	"fmt"
	"io"
	"io/fs"
	"strconv"
	"strings"
	"unicode/utf8"
	"unsafe"
)

// Compact 32-bit grammatical tag bitmask layout (matches tools/build_dicts.py).
const (
	morphPOSMask     uint32 = 0x1F
	morphGenderShift        = 5
	morphGenderMask  uint32 = 0x3 << morphGenderShift
	morphNumberShift        = 7
	morphNumberMask  uint32 = 0x3 << morphNumberShift
	morphCasesShift         = 9
	morphCasesMask   uint32 = 0x3F << morphCasesShift

	morphFlagAnim        uint32 = 1 << 15
	morphFlagInan        uint32 = 1 << 16
	morphFlagPltm        uint32 = 1 << 17
	morphFlagSgtm        uint32 = 1 << 18
	morphFlagFixd        uint32 = 1 << 19
	morphFlagHasNoun     uint32 = 1 << 20
	morphFlagHasAdj      uint32 = 1 << 21
	morphFlagHasVerb     uint32 = 1 << 22
	morphFlagHasGoverned uint32 = 1 << 23
	morphFlagHasNomnNoun uint32 = 1 << 24
	morphFlagHasPropn    uint32 = 1 << 25
)

// 6-bit Russian grammatical case bitmask values.
const (
	caseBitNomn uint8 = 1 << 0
	caseBitGent uint8 = 1 << 1
	caseBitDatv uint8 = 1 << 2
	caseBitAccs uint8 = 1 << 3
	caseBitAblt uint8 = 1 << 4
	caseBitLoct uint8 = 1 << 5
)

var posNames = [...]string{
	"",     // 0
	"NOUN", // 1
	"ADJF", // 2
	"ADJS", // 3
	"COMP", // 4
	"VERB", // 5
	"INFN", // 6
	"PRTF", // 7
	"PRTS", // 8
	"GRND", // 9
	"NUMR", // 10
	"ADVB", // 11
	"NPRO", // 12
	"PRED", // 13
	"PREP", // 14
	"CONJ", // 15
	"PRCL", // 16
	"INTJ", // 17
}

var genderNames = [...]string{"", "masc", "femn", "neut"}
var numberNames = [...]string{"", "sing", "plur", "both"}
var caseNames = [...]string{"nomn", "gent", "datv", "accs", "ablt", "loct"}

// Preposition government table: maps Russian prepositions to the 6-bit mask of governed cases.
var prepGovernedCases = map[string]uint8{
	"с":      caseBitGent | caseBitAblt,
	"со":     caseBitGent | caseBitAblt,
	"к":      caseBitDatv,
	"ко":     caseBitDatv,
	"в":      caseBitAccs | caseBitLoct,
	"во":     caseBitAccs | caseBitLoct,
	"на":     caseBitAccs | caseBitLoct,
	"по":     caseBitDatv | caseBitAccs | caseBitLoct,
	"о":      caseBitAccs | caseBitLoct,
	"об":     caseBitAccs | caseBitLoct,
	"обо":    caseBitAccs | caseBitLoct,
	"при":    caseBitLoct,
	"от":     caseBitGent,
	"ото":    caseBitGent,
	"из":     caseBitGent,
	"изо":    caseBitGent,
	"до":     caseBitGent,
	"без":    caseBitGent,
	"безо":   caseBitGent,
	"у":      caseBitGent,
	"для":    caseBitGent,
	"кроме":  caseBitGent,
	"около":  caseBitGent,
	"после":  caseBitGent,
	"против": caseBitGent,
	"из-за":  caseBitGent,
	"из-под": caseBitGent,
	"под":    caseBitAccs | caseBitAblt,
	"подо":   caseBitAccs | caseBitAblt,
	"за":     caseBitAccs | caseBitAblt,
	"над":    caseBitAblt,
	"надо":   caseBitAblt,
	"перед":  caseBitAblt,
	"передо": caseBitAblt,
	"между":  caseBitAblt,
	"про":    caseBitAccs,
	"через":  caseBitAccs,
	"сквозь": caseBitAccs,
}

// Candidate represents one valid dictionary (lemma, POS, cases) parse for a wordform.
type Candidate struct {
	Lemma string   `json:"lemma"`
	POS   string   `json:"pos"`
	Cases []string `json:"cases,omitempty"`
}

// WordInfo holds compact morphological analysis results for a Russian wordform (v0.3).
type WordInfo struct {
	Word        string      `json:"word"`
	Lemma       string      `json:"lemma"`
	Source      string      `json:"source"` // override | dict | rule | oov
	Known       bool        `json:"known"`  // true when grammatical tags came from morph dictionary
	Inferred    bool        `json:"inferred,omitempty"` // true when tags were inferred from the lemma's inflection paradigm (word absent from morph table)
	POS         string      `json:"pos"`
	Gender      string      `json:"gender,omitempty"`
	Number      string      `json:"number,omitempty"`
	Cases       []string    `json:"cases,omitempty"`
	Animacy     string      `json:"animacy,omitempty"`
	Pltm        bool        `json:"pltm,omitempty"`
	Sgtm        bool        `json:"sgtm,omitempty"`
	Fixd        bool        `json:"fixd,omitempty"`
	HasNoun     bool        `json:"has_noun,omitempty"`
	HasNomnNoun bool        `json:"has_nomn_noun,omitempty"`
	IsHomonym   bool        `json:"is_homonym,omitempty"`
	Candidates  []Candidate `json:"candidates,omitempty"`
}

func decodeCases6(mask uint8) []string {
	if mask == 0 {
		return nil
	}
	out := make([]string, 0, 4)
	for i, name := range caseNames {
		if mask&(1<<uint(i)) != 0 {
			out = append(out, name)
		}
	}
	return out
}

func loadMorphFS(fsys fs.FS) (map[string]uint32, error) {
	name := firstExistingFS(fsys, "morph.tsv.gz", "morph.tsv")
	if name == "" {
		return map[string]uint32{}, nil
	}
	rc, err := openMaybeGzipFS(fsys, name)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", name, err)
	}
	defer rc.Close()

	raw, err := io.ReadAll(rc)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", name, err)
	}
	if len(raw) == 0 {
		return map[string]uint32{}, nil
	}
	data := unsafe.String(unsafe.SliceData(raw), len(raw))
	out := make(map[string]uint32, 130000)

	start := 0
	for start < len(data) {
		nl := strings.IndexByte(data[start:], '\n')
		var line string
		if nl < 0 {
			line = data[start:]
			start = len(data)
		} else {
			line = data[start : start+nl]
			start += nl + 1
		}
		if len(line) == 0 || line[0] == '#' {
			continue
		}
		if line[len(line)-1] == '\r' {
			line = line[:len(line)-1]
		}
		tab := strings.IndexByte(line, '\t')
		if tab <= 0 || tab+1 >= len(line) {
			continue
		}
		v, err := strconv.ParseUint(line[tab+1:], 16, 32)
		if err != nil {
			continue
		}
		out[line[:tab]] = uint32(v)
	}
	return out, nil
}

func loadAltLemmasFS(fsys fs.FS) (map[string]string, error) {
	name := firstExistingFS(fsys, "alt_lemmas.tsv.gz", "alt_lemmas.tsv")
	if name == "" {
		return map[string]string{}, nil
	}
	rc, err := openMaybeGzipFS(fsys, name)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", name, err)
	}
	defer rc.Close()

	raw, err := io.ReadAll(rc)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", name, err)
	}
	if len(raw) == 0 {
		return map[string]string{}, nil
	}
	data := unsafe.String(unsafe.SliceData(raw), len(raw))
	out := make(map[string]string, 12000)

	start := 0
	for start < len(data) {
		nl := strings.IndexByte(data[start:], '\n')
		var line string
		if nl < 0 {
			line = data[start:]
			start = len(data)
		} else {
			line = data[start : start+nl]
			start += nl + 1
		}
		if len(line) == 0 || line[0] == '#' {
			continue
		}
		if line[len(line)-1] == '\r' {
			line = line[:len(line)-1]
		}
		tab := strings.IndexByte(line, '\t')
		if tab <= 0 || tab+1 >= len(line) {
			continue
		}
		out[line[:tab]] = line[tab+1:]
	}
	return out, nil
}

func parseAltCandidates(raw string) []Candidate {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, "|")
	out := make([]Candidate, 0, len(parts))
	for _, p := range parts {
		c1 := strings.IndexByte(p, ':')
		if c1 <= 0 {
			continue
		}
		rest := p[c1+1:]
		c2 := strings.IndexByte(rest, ':')
		if c2 < 0 {
			out = append(out, Candidate{Lemma: p[:c1], POS: rest})
			continue
		}
		mask6, _ := strconv.ParseUint(rest[c2+1:], 16, 8)
		out = append(out, Candidate{
			Lemma: p[:c1],
			POS:   rest[:c2],
			Cases: decodeCases6(uint8(mask6)),
		})
	}
	return out
}

func (l *Lemmatizer) lookupMorphBits(w string) (uint32, bool) {
	l.ensureMorph()
	if len(l.morph) == 0 {
		return 0, false
	}
	if bits, ok := l.morph[w]; ok {
		return bits, true
	}
	if wn := normYo(w); wn != w {
		if bits, ok := l.morph[wn]; ok {
			return bits, true
		}
	}
	return 0, false
}

// Analyze returns compact morphological information (POS, gender, number, cases, flags, and
// homonym candidates) for a single wordform using preloaded dictionary tables.
func (l *Lemmatizer) Analyze(word string) WordInfo {
	w := strings.Trim(strings.ToLower(word), "-")
	if w == "" {
		return WordInfo{Source: SourceOOV}
	}
	lemma, src := l.LookupWord(w)
	info := WordInfo{
		Word:   w,
		Lemma:  lemma,
		Source: src,
	}

	bits, ok := l.lookupMorphBits(w)
	inferred := false
	if !ok {
		bits, ok = l.inferMorphBits(w)
		inferred = ok
	}
	if !ok {
		return info
	}

	info.Known = true
	info.Inferred = inferred
	posIdx := int(bits & morphPOSMask)
	if posIdx > 0 && posIdx < len(posNames) {
		info.POS = posNames[posIdx]
	}
	genIdx := int((bits & morphGenderMask) >> morphGenderShift)
	if genIdx > 0 && genIdx < len(genderNames) {
		info.Gender = genderNames[genIdx]
	}
	numIdx := int((bits & morphNumberMask) >> morphNumberShift)
	if numIdx > 0 && numIdx < len(numberNames) {
		info.Number = numberNames[numIdx]
	}
	cases6 := uint8((bits & morphCasesMask) >> morphCasesShift)
	info.Cases = decodeCases6(cases6)
	if bits&morphFlagAnim != 0 {
		info.Animacy = "anim"
	} else if bits&morphFlagInan != 0 {
		info.Animacy = "inan"
	}
	info.Pltm = bits&morphFlagPltm != 0
	info.Sgtm = bits&morphFlagSgtm != 0
	info.Fixd = bits&morphFlagFixd != 0
	info.HasNoun = bits&morphFlagHasNoun != 0
	info.HasNomnNoun = bits&morphFlagHasNomnNoun != 0

	var rawAlt string
	if len(l.altLemmas) > 0 {
		if s, found := l.altLemmas[w]; found {
			rawAlt = s
		} else if wn := normYo(w); wn != w {
			rawAlt = l.altLemmas[wn]
		}
	}
	if rawAlt != "" {
		info.Candidates = parseAltCandidates(rawAlt)
		info.IsHomonym = len(info.Candidates) >= 2
	} else if info.POS != "" {
		info.Candidates = []Candidate{{
			Lemma: info.Lemma,
			POS:   info.POS,
			Cases: info.Cases,
		}}
	}
	return info
}

// WordCandidates returns all valid dictionary (lemma, POS, cases) parses for word.
// For unambiguous words returns a single-element slice; for homonyms like "пил" returns
// both "пить" (VERB) and "пила" (NOUN, gent).
func (l *Lemmatizer) WordCandidates(word string) []Candidate {
	return l.Analyze(word).Candidates
}

// PrepGoverns reports whether Russian preposition prep grammatically governs nextWord
// (e.g. PrepGoverns("с", "цинком") == true, PrepGoverns("к", "применению") == true,
// PrepGoverns("с", "1000") == false, PrepGoverns("в", "д") == false).
func (l *Lemmatizer) PrepGoverns(prep, nextWord string) bool {
	p := normYo(strings.ToLower(strings.TrimSpace(prep)))
	wantCases, ok := prepGovernedCases[p]
	if !ok {
		return false
	}
	nw := strings.Trim(strings.ToLower(strings.TrimSpace(nextWord)), "-")
	if nw == "" || utf8.RuneCountInString(nw) <= 1 {
		return false
	}
	bits, found := l.morphBits(nw)
	if !found {
		return false
	}
	if bits&morphFlagHasGoverned == 0 {
		return false
	}
	cases6 := uint8((bits & morphCasesMask) >> morphCasesShift)
	return (cases6 & wantCases) != 0
}
