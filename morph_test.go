package rulemma_test

import (
	"sync"
	"testing"
	"testing/fstest"

	"github.com/DimaKorshikov/rulemma"
)

func TestAnalyzeAndHomonymCandidates(t *testing.T) {
	lem := mustLoadTest(t)

	// Known unambiguous noun
	info := lem.Analyze("кроссовки")
	if !info.Known || info.POS != "NOUN" || info.Lemma != "кроссовок" || !info.HasNoun {
		t.Errorf("Analyze(кроссовки) = %+v", info)
	}

	// Homonym "пил" ("виды пил" vs "пил сок"): must include both "пить" (VERB) and "пила" (NOUN)
	pil := lem.Analyze("пил")
	if !pil.Known || !pil.IsHomonym || !pil.HasNoun {
		t.Fatalf("Analyze(пил) expected known homonym with noun candidate, got %+v", pil)
	}
	hasPilaNoun := false
	hasPitVerb := false
	for _, c := range pil.Candidates {
		if c.Lemma == "пила" && c.POS == "NOUN" {
			hasPilaNoun = true
		}
		if c.Lemma == "пить" && c.POS == "VERB" {
			hasPitVerb = true
		}
	}
	if !hasPilaNoun || !hasPitVerb {
		t.Errorf("Analyze(пил) candidates missing пила/пить: %+v", pil.Candidates)
	}

	// Homonym "мелки": мелок (NOUN) + мелкий (ADJS)
	melki := lem.Analyze("мелки")
	hasMelok := false
	for _, c := range melki.Candidates {
		if c.Lemma == "мелок" && c.POS == "NOUN" {
			hasMelok = true
		}
	}
	if !hasMelok {
		t.Errorf("Analyze(мелки) expected мелок:NOUN candidate, got %+v", melki.Candidates)
	}

	// Homonym "стали": сталь (NOUN) + стать (VERB)
	stali := lem.Analyze("стали")
	hasStal := false
	hasStat := false
	for _, c := range stali.Candidates {
		if c.Lemma == "сталь" && c.POS == "NOUN" {
			hasStal = true
		}
		if c.Lemma == "стать" && c.POS == "VERB" {
			hasStat = true
		}
	}
	if !hasStal || !hasStat {
		t.Errorf("Analyze(стали) expected сталь/стать, got %+v", stali.Candidates)
	}

	// Homonym "ваз": ваз (NOUN) + ваза (NOUN)
	vaz := lem.Analyze("ваз")
	hasVaza := false
	for _, c := range vaz.Candidates {
		if c.Lemma == "ваза" && c.POS == "NOUN" {
			hasVaza = true
		}
	}
	if !hasVaza {
		t.Errorf("Analyze(ваз) expected ваза:NOUN candidate, got %+v", vaz.Candidates)
	}

	// Grammatical flags: Pltm, Sgtm, Fixd
	if sc := lem.Analyze("ножницы"); !sc.Pltm || sc.POS != "NOUN" {
		t.Errorf("Analyze(ножницы) expected Pltm NOUN, got %+v", sc)
	}
	if milk := lem.Analyze("молоко"); !milk.Sgtm || milk.POS != "NOUN" {
		t.Errorf("Analyze(молоко) expected Sgtm NOUN, got %+v", milk)
	}
	if coat := lem.Analyze("пальто"); !coat.Fixd || coat.POS != "NOUN" {
		t.Errorf("Analyze(пальто) expected Fixd NOUN, got %+v", coat)
	}

	// OOV Latin / unseen brand
	oov := lem.Analyze("abrakadabra123")
	if oov.Known || oov.POS != "" || oov.Source != rulemma.SourceOOV {
		t.Errorf("Analyze(OOV) expected Known=false Source=oov, got %+v", oov)
	}
}

func TestPrepGoverns(t *testing.T) {
	lem := mustLoadTest(t)

	trueCases := [][2]string{
		{"с", "цинком"},
		{"с", "сахаром"},
		{"с", "горки"},
		{"с", "молоком"},
		{"с", "кофе"}, // Fixd noun governed in all cases
		{"к", "применению"},
		{"к", "школе"},
		{"в", "москве"},
		{"в", "дом"},
		{"в", "метро"}, // Fixd noun
		{"без", "сахара"},
		{"для", "детей"},
		{"по", "стали"},
	}
	for _, tc := range trueCases {
		if !lem.PrepGoverns(tc[0], tc[1]) {
			t.Errorf("PrepGoverns(%q, %q) = false, want true", tc[0], tc[1])
		}
	}

	falseCases := [][2]string{
		{"с", "1000"},   // "витамин с 1000"
		{"с", "купить"}, // infinitive verb
		{"к", "для"},    // "витамин к для чего"
		{"в", "д"},      // "витамины в и д" (single letter)
		{"в", "и"},      // single letter
		{"с", "а"},      // single letter
		{"к", "москве"}, // datv? wait: "москве" IS datv/loct! Use non-datv: "к сахара"
		{"к", "сахара"},
		{"без", "цинком"}, // ablt != gent
		{"xyz", "цинком"}, // unknown prep
	}
	for _, tc := range falseCases {
		if tc[0] == "к" && tc[1] == "москве" {
			continue
		}
		if lem.PrepGoverns(tc[0], tc[1]) {
			t.Errorf("PrepGoverns(%q, %q) = true, want false", tc[0], tc[1])
		}
	}
}

func TestLoadFSBackwardCompatibleWithoutV03Files(t *testing.T) {
	fsys := fstest.MapFS{
		"lemmas.tsv": &fstest.MapFile{Data: []byte("кроссовки\tкроссовок\n")},
	}
	lem, err := rulemma.LoadFS(fsys, rulemma.Options{})
	if err != nil {
		t.Fatalf("LoadFS without v0.3 files failed: %v", err)
	}
	if got := lem.Word("кроссовки"); got != "кроссовок" {
		t.Errorf("Word = %q, want кроссовок", got)
	}
	if info := lem.Analyze("кроссовки"); info.Known {
		t.Errorf("expected Known=false without morph.tsv.gz, got %+v", info)
	}
	if got, ok := lem.Inflect("кроссовки", "gent", ""); ok || got != "кроссовки" {
		t.Errorf("expected ok=false without paradigms.tsv.gz, got (%q, %v)", got, ok)
	}
}

func TestConcurrentV03LazyLoad(t *testing.T) {
	lem := mustLoadTest(t)
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				if info := lem.Analyze("пил"); !info.Known || !info.IsHomonym {
					t.Errorf("concurrent Analyze(пил) failed: %+v", info)
					return
				}
				if !lem.PrepGoverns("с", "цинком") {
					t.Errorf("concurrent PrepGoverns failed")
					return
				}
				if res, ok := lem.InflectPhrase("стиральная машина LG F2V5", "accs", ""); !ok || res != "стиральную машину LG F2V5" {
					t.Errorf("concurrent InflectPhrase failed: (%q, %v)", res, ok)
					return
				}
				if got := lem.AgreeNumber(21, "товар"); got != "21 товар" {
					t.Errorf("concurrent AgreeNumber failed: %q", got)
					return
				}
			}
		}()
	}
	wg.Wait()
}
