package rulemma_test

import (
	"testing"

	"github.com/DimaKorshikov/rulemma"
)

func mustLoadTest(t *testing.T) *rulemma.Lemmatizer {
	t.Helper()
	lem, err := rulemma.Load("data")
	if err != nil {
		t.Fatalf("Load(data): %v", err)
	}
	return lem
}

func TestNormalizeHomoglyphs(t *testing.T) {
	cases := [][2]string{
		{"cм", "см"},
		{"5cм", "5см"},
		{"рrо", "pro"},
		{"intеl", "intel"},
		{"аsus", "asus"},
		{"оzon", "ozon"},
		{"ryzеn", "ryzen"},
		{"молоко 100%", "молоко 100%"},
		{"Samsung Galaxy S24", "Samsung Galaxy S24"},
		{"Кабель 5cм intеl рrо", "Кабель 5см intel pro"},
	}
	for _, tc := range cases {
		got := rulemma.NormalizeHomoglyphs(tc[0])
		if got != tc[1] {
			t.Errorf("NormalizeHomoglyphs(%q) = %q, want %q", tc[0], got, tc[1])
		}
	}
}

func TestTechnicalProfileCollisionsAndEquivalences(t *testing.T) {
	lem := mustLoadTest(t)
	tech := rulemma.KeyOptions{Semantic: true, Profile: "technical"}

	// 1. Must NOT collide (critical commercial / spec differences)
	neqPairs := [][2]string{
		{"бензопила 2,6 л.с.", "бензопила 6,2 л.с."},
		{"плитка 100×100 см", "плитка 100 см"},
		{"коробка 500×500 мм", "коробка 500×500×500 мм"},
		{"блок питания 3,3 В", "блок питания 3 В"},
		{"зарядка 12 В", "зарядка 3 в 1"},
		{"зимние ботинки б/у", "зимние ботинки"},
		{"игры 18+", "игры 18"},
		{"книга c++", "книга c"},
		{"телефон 8/256", "телефон 8/128"},
		{"шланг до 50 см", "шланг от 50 см"},
		{"шланг до 50 см", "шланг 50 см"},
	}
	for _, p := range neqPairs {
		k1 := lem.KeyWithOptions(p[0], tech)
		k2 := lem.KeyWithOptions(p[1], tech)
		if k1 == k2 {
			t.Errorf("technical profile collision: %q and %q both -> %q", p[0], p[1], k1)
		}
	}

	// 2. Must MATCH (normalized technical equivalents)
	eqPairs := [][2]string{
		{"термос 0,5 л", "термос 0.5 л"},
		{"термос 0,5л", "термос 0.5 л"},
		{"плитка 100×100 см", "плитка 100 x 100 cм"},
		{"винт 5 мм", "винт 5мм"},
		{"ssd m.2", "ssd м.2"},
		{"диск m.2.", "диск m.2"},
		{"холодильник a+++", "холодильник A+++"},
		{"триммер 2,6 л.с.", "триммер 2.6 л.с"},
		{"велосипед б/у", "велосипед б / у"},
		{"аккумулятор 12 В", "аккумулятор 12в"},
	}
	for _, p := range eqPairs {
		k1 := lem.KeyWithOptions(p[0], tech)
		k2 := lem.KeyWithOptions(p[1], tech)
		if k1 != k2 {
			t.Errorf("technical profile mismatch: %q -> %q != %q -> %q", p[0], k1, p[1], k2)
		}
	}

	// 3. Exact expected keys on subtle contextual cases
	if got := lem.KeyWithOptions("зимние ботинки б/у", tech); got != "б/у ботинок зима" {
		t.Errorf("б/у key = %q, want %q", got, "б/у ботинок зима")
	}
	if got := lem.KeyWithOptions("шампунь 3 в 1", tech); got != "1 3 шампунь" {
		t.Errorf("3 в 1 key = %q, want %q", got, "1 3 шампунь")
	}
	if got := lem.KeyWithOptions("ботинки 42 в москве", tech); got != "42 ботинок москва" {
		t.Errorf("42 в москве key = %q, want %q", got, "42 ботинок москва")
	}
	if got := lem.KeyWithOptions("аккумулятор 12 В", tech); got != "12 аккумулятор в" {
		t.Errorf("12 В key = %q, want %q", got, "12 аккумулятор в")
	}
}

func TestPrepositionBindingsAndGuards(t *testing.T) {
	lem := mustLoadTest(t)

	// Negation preposition binding: "без сахара с молоком" != "с сахаром без молока"
	negOpts := rulemma.KeyOptions{
		Semantic:             true,
		Profile:              "technical",
		PreservePrepBindings: true,
	}
	k1 := lem.KeyWithOptions("кофе без сахара с молоком", negOpts)
	k2 := lem.KeyWithOptions("кофе с сахаром без молока", negOpts)
	if k1 == k2 {
		t.Fatalf("PreservePrepBindings failed: both -> %q", k1)
	}
	if k1 != "без_сахар кофе молоко" {
		t.Errorf("got %q, want %q", k1, "без_сахар кофе молоко")
	}
	if k2 != "без_молоко кофе сахар" {
		t.Errorf("got %q, want %q", k2, "без_молоко кофе сахар")
	}

	// Purpose preposition binding: "пластиковые окна" != "пластик для окон"
	purpOpts := rulemma.KeyOptions{
		Semantic:        true,
		Profile:         "technical",
		BindPurposePrep: true,
	}
	p1 := lem.KeyWithOptions("пластиковые окна", purpOpts)
	p2 := lem.KeyWithOptions("пластик для окон", purpOpts)
	if p1 == p2 {
		t.Fatalf("BindPurposePrep failed: both -> %q", p1)
	}
	if p2 != "для_окно пластик" {
		t.Errorf("BindPurposePrep got %q, want %q", p2, "для_окно пластик")
	}

	// KeepWords head protection: "зеркальный пластик" != "пластиковое зеркало"
	keepOpts := rulemma.KeyOptions{
		Semantic:  true,
		Profile:   "technical",
		KeepWords: rulemma.MakeStopWords([]string{"пластик"}),
	}
	w1 := lem.KeyWithOptions("зеркальный пластик", keepOpts)
	w2 := lem.KeyWithOptions("пластиковое зеркало", keepOpts)
	if w1 == w2 {
		t.Fatalf("KeepWords failed: both -> %q", w1)
	}

	// SplitHyphens: "онлайн-курсы" == "онлайн курсы"
	hyOpts := rulemma.KeyOptions{
		Semantic: true,
		Profile:  "technical",
		Tokenizer: rulemma.TokenizerOptions{
			SplitHyphens: true,
		},
	}
	if a, b := lem.KeyWithOptions("онлайн-курсы python", hyOpts), lem.KeyWithOptions("онлайн курсы python", hyOpts); a != b {
		t.Errorf("SplitHyphens mismatch: %q != %q", a, b)
	}

	// CompactCodes: "p-6000" == "p 6000" == "p6000", "мб-23" == "мб 23", "ecam 220" == "ecam220"
	codeOpts := rulemma.KeyOptions{
		Semantic: true,
		Profile:  "technical",
		Tokenizer: rulemma.TokenizerOptions{
			CompactCodes: true,
		},
	}
	for _, pair := range [][2]string{
		{"кроссовки nike p-6000", "кроссовки nike p 6000"},
		{"кроссовки nike p-6000", "кроссовки nike p6000"},
		{"мотоблок нева мб-23", "мотоблок нева мб 23"},
		{"кофемашина delonghi ecam 220", "кофемашина delonghi ecam220"},
	} {
		a := lem.KeyWithOptions(pair[0], codeOpts)
		b := lem.KeyWithOptions(pair[1], codeOpts)
		if a != b {
			t.Errorf("CompactCodes mismatch: %q -> %q != %q -> %q", pair[0], a, pair[1], b)
		}
	}
	// CompactCodes must NOT merge common Russian prepositions with numbers ("в 2025", "до 50")
	if got := lem.KeyWithOptions("отдых в 2025 году", codeOpts); got != "2025 год отдых" {
		t.Errorf("CompactCodes polluted preposition+year: got %q", got)
	}
}

func TestDefaultKeyBackwardCompatibility(t *testing.T) {
	lem := mustLoadTest(t)
	samples := []string{
		"Купить зимние кроссовки Найк",
		"шоколад без сахара",
		"пластиковые окна",
		"плитка 100x100 см",
	}
	for _, s := range samples {
		if a, b := lem.LemmaKey(s), lem.KeyWithOptions(s, rulemma.KeyOptions{}); a != b {
			t.Errorf("default LemmaKey drift on %q: %q vs %q", s, a, b)
		}
		if a, b := lem.SemanticKey(s), lem.KeyWithOptions(s, rulemma.KeyOptions{Semantic: true}); a != b {
			t.Errorf("default SemanticKey drift on %q: %q vs %q", s, a, b)
		}
	}
}
