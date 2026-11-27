package rulemma

import "strings"

// morphBits returns dictionary morphology bits for w, falling back to paradigm-based inference
// for word forms absent from morph.tsv.gz (e.g. "дипломом", "выдачей", "фритюрнице").
func (l *Lemmatizer) morphBits(w string) (uint32, bool) {
	if bits, ok := l.lookupMorphBits(w); ok {
		return bits, true
	}
	return l.inferMorphBits(w)
}

// inferMorphBits infers POS, gender, number and case bits for a word form that is missing from the
// morph table:
//  1. Exact match of the form against its lemma's inflection paradigm in paradigms.tsv.gz;
//  2. Regular superlative adjective endings on -ш- (высшим, лучшего, крупнейших);
//  3. Compound/prefixed Cyrillic words whose known tail (>= 5 runes, e.g. поддержкой in господдержкой)
//     exists as NOUN/ADJF/PRTF in morph.tsv.gz.
func (l *Lemmatizer) inferMorphBits(word string) (uint32, bool) {
	w := normYo(strings.Trim(strings.ToLower(word), "-"))
	if w == "" || !isPureCyrillicWord(w) {
		return 0, false
	}
	lemma := l.Word(w)
	if lemma != "" {
		if bits, ok := l.inferFromParadigm(w, lemma); ok {
			return bits, true
		}
	}
	if bits, ok := l.inferSuperlativeAdj(w); ok {
		return bits, true
	}
	if bits, ok := l.inferFromCompoundTail(w); ok {
		return bits, true
	}
	return 0, false
}

func (l *Lemmatizer) inferSuperlativeAdj(w string) (uint32, bool) {
	// Regular Russian superlative adjectives in -ш- whose nominative -ший exists in l.lemmas (высший->высокий).
	type sufSpec struct {
		suf    string
		cases  uint8
		num    uint32 // 1=sing, 2=plur, 3=both
		gender uint32 // 0=none, 1=masc, 2=femn, 3=neut
	}
	specs := [...]sufSpec{
		{"шими", caseBitAblt, 2, 0},
		{"шего", caseBitGent | caseBitAccs, 1, 0},
		{"шему", caseBitDatv, 1, 0},
		{"шим", caseBitAblt | caseBitDatv, 3, 0},
		{"ших", caseBitGent | caseBitAccs | caseBitLoct, 2, 0},
		{"шем", caseBitLoct, 1, 0},
		{"шей", caseBitGent | caseBitDatv | caseBitAblt | caseBitLoct, 1, 2},
		{"шую", caseBitAccs, 1, 2},
		{"шая", caseBitNomn, 1, 2},
		{"шее", caseBitNomn | caseBitAccs, 1, 3},
		{"шие", caseBitNomn | caseBitAccs, 2, 0},
		{"ший", caseBitNomn | caseBitAccs, 1, 1},
	}
	for _, sp := range specs {
		if !strings.HasSuffix(w, sp.suf) {
			continue
		}
		stem := strings.TrimSuffix(w, sp.suf)
		if len([]rune(stem)) < 2 {
			continue
		}
		if _, ok := l.lemmas.Get(stem + "ший"); ok {
			bits := uint32(2) | (sp.gender << morphGenderShift) | (sp.num << morphNumberShift) |
				(uint32(sp.cases) << morphCasesShift) | morphFlagHasAdj | morphFlagHasGoverned
			return bits, true
		}
	}
	return 0, false
}

func (l *Lemmatizer) inferFromCompoundTail(w string) (uint32, bool) {
	runes := []rune(w)
	n := len(runes)
	if n < 7 {
		return 0, false
	}
	// Try longest known suffix >= 5 runes with at least a 2-rune Cyrillic prefix (e.g. гос+поддержкой, авто+мойкой).
	for cut := 2; cut <= n-5; cut++ {
		tail := string(runes[cut:])
		if bits, ok := l.lookupMorphBits(tail); ok {
			pos := bits & morphPOSMask
			if pos == 1 || pos == 2 || pos == 7 { // NOUN, ADJF, PRTF
				return bits, true
			}
		}
	}
	return 0, false
}

func (l *Lemmatizer) inferFromParadigm(w, lemma string) (uint32, bool) {
	ps := l.getParadigmStore()
	ref, found := ps.getWord(lemma)
	if !found {
		return 0, false
	}

	var bits uint32
	kind := ref.flags & pfKindMask
	if ref.flags&pfAnim != 0 {
		bits |= morphFlagAnim
	} else if ref.flags&pfInan != 0 {
		bits |= morphFlagInan
	}
	if ref.flags&pfPltm != 0 {
		bits |= morphFlagPltm
	}
	if ref.flags&pfSgtm != 0 {
		bits |= morphFlagSgtm
	}

	// Indeclinable (Fixd): the form must equal the lemma; all cases are possible.
	if ref.flags&pfFixd != 0 || ref.pid == 0 {
		if w != lemma {
			return 0, false
		}
		bits |= morphFlagFixd | uint32(caseBitNomn|caseBitGent|caseBitDatv|caseBitAccs|caseBitAblt|caseBitLoct)<<morphCasesShift
		bits |= uint32(3) << morphNumberShift
		if kind == pfKindAdj {
			bits |= 2 | morphFlagHasAdj | morphFlagHasGoverned
		} else {
			g := uint32((ref.flags & pfGenderMask) >> pfGenderShift)
			bits |= 1 | g<<morphGenderShift | morphFlagHasNoun | morphFlagHasNomnNoun | morphFlagHasGoverned
		}
		return bits, true
	}
	if int(ref.pid) >= len(ps.defs) {
		return 0, false
	}
	def := ps.defs[ref.pid]

	var cases uint8
	var sing, plur bool
	var genders [4]bool
	for slot := range def.endings {
		form, ok := applyParadigmSlot(lemma, def, slot)
		if !ok || form != w {
			continue
		}
		switch def.kind {
		case 'N':
			if slot < 6 {
				cases |= 1 << uint(slot)
				sing = true
			} else if slot < 12 {
				cases |= 1 << uint(slot-6)
				plur = true
			}
		case 'A':
			switch {
			case slot < 18:
				cases |= 1 << uint(slot%6)
				sing = true
				genders[1+slot/6] = true
			case slot < 24:
				cases |= 1 << uint(slot-18)
				plur = true
			case slot == 24:
				cases |= caseBitAccs
				sing = true
				genders[1] = true
			case slot == 25:
				cases |= caseBitAccs
				plur = true
			}
		}
	}
	if cases == 0 {
		return 0, false
	}

	bits |= uint32(cases) << morphCasesShift
	switch {
	case sing && plur:
		bits |= uint32(3) << morphNumberShift
	case plur:
		bits |= uint32(2) << morphNumberShift
	default:
		bits |= uint32(1) << morphNumberShift
	}
	if def.kind == 'N' {
		g := uint32((ref.flags & pfGenderMask) >> pfGenderShift)
		bits |= 1 | g<<morphGenderShift | morphFlagHasNoun | morphFlagHasGoverned
		if cases&caseBitNomn != 0 {
			bits |= morphFlagHasNomnNoun
		}
	} else {
		bits |= 2 | morphFlagHasAdj | morphFlagHasGoverned
		gCount, gIdx := 0, 0
		for i := 1; i <= 3; i++ {
			if genders[i] {
				gCount++
				gIdx = i
			}
		}
		if gCount == 1 && !plur {
			bits |= uint32(gIdx) << morphGenderShift
		}
	}
	return bits, true
}
