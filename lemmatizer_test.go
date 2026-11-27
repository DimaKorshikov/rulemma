package rulemma

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
)

func loadTestLem(t *testing.T) *Lemmatizer {
	t.Helper()
	lem, err := Load("data")
	if err != nil {
		t.Fatalf("Load(data): %v", err)
	}
	return lem
}

func TestLoadStats(t *testing.T) {
	lem := loadTestLem(t)
	st := lem.Stats()
	if st.Lemmas < 150000 {
		t.Errorf("expected >=150k Cyrillic lemmas, got %d", st.Lemmas)
	}
	if st.AdjForms < 50000 {
		t.Errorf("expected >=50k adj_forms, got %d", st.AdjForms)
	}
	if st.Types < 3000 {
		t.Errorf("expected >=3k types, got %d", st.Types)
	}
	t.Logf("Loaded stats: %+v", st)
}

func TestEcommerceBugFixes(t *testing.T) {
	lem := loadTestLem(t)
	cases := []struct {
		word string
		want string
	}{
		// Brand & loanword bugs fixed over pymorphy3
		{"сяоми", "сяоми"},
		{"ксиоми", "ксиоми"},
		{"хуавей", "хуавей"},
		{"хендай", "хендай"},
		{"хонор", "хонор"},
		{"реалми", "реалми"},
		{"галакси", "галакси"},
		{"макита", "макита"},
		{"ресанта", "ресанта"},
		{"хутер", "хутер"},
		{"дрими", "дрими"},
		{"сони", "сони"},
		{"тефаль", "тефаль"},
		{"индезит", "индезит"},
		{"аквафор", "аквафор"},
		{"голдстар", "голдстар"},
		{"диор", "диор"},
		{"золла", "золла"},
		{"эконика", "эконика"},
		{"аскона", "аскона"},
		{"комус", "комус"},
		{"летуаль", "летуаль"},
		{"солгар", "солгар"},
		{"старлайн", "старлайн"},

		// Product noun collisions fixed over pymorphy3
		{"спрей", "спрей"},
		{"кофемашина", "кофемашина"},
		{"кружка", "кружка"},
		{"кружки", "кружка"},
		{"майка", "майка"},
		{"планка", "планка"},
		{"петля", "петля"},
		{"математика", "математика"},
		{"поло", "поло"},
		{"худи", "худи"},
		{"боди", "боди"},
		{"кигуруми", "кигуруми"},
		{"эндуро", "эндуро"},
		{"душ", "душ"},
		{"фаркоп", "фаркоп"},
		{"сайдинг", "сайдинг"},
		{"рестайлинг", "рестайлинг"},
		{"трансформер", "трансформер"},
		{"гарнитур", "гарнитур"},
		{"лайт", "лайт"},
		{"туя", "туя"},
		{"калина", "калина"},
		{"астра", "астра"},
		{"печенье", "печенье"},
		{"варенье", "варенье"},
		{"сиденье", "сиденье"},

		// Pharma INN words not hallucinated as verbs
		{"афобазол", "афобазол"},
		{"полисорб", "полисорб"},
		{"энтерофурил", "энтерофурил"},
		{"миноксидил", "миноксидил"},
		{"фенистил", "фенистил"},
		{"кардиомагнил", "кардиомагнил"},
		{"бисопролол", "бисопролол"},
		{"силденафил", "силденафил"},

		// True inflections MUST NOT be broken (no regressions!)
		{"комаров", "комар"},
		{"зубов", "зуб"},
		{"огурцов", "огурец"},
		{"муравьев", "муравей"},
		{"газа", "газ"},
		{"полива", "полив"},
		{"калия", "калий"},
		{"белые", "белый"},
		{"чёрная", "черный"},
		{"зимние", "зимний"},
		{"детская", "детский"},
		{"красная", "красный"},
		{"кроссовки", "кроссовок"},
		{"учебники", "учебник"},
		{"айфоны", "айфон"},
		{"телевизоры", "телевизор"},
		{"мороженое", "мороженое"},
		{"бодиметрии", "бодиметрия"},

		// Residual OpenCorpora / adj_forms edge cases verified clean
		{"сад", "сад"},
		{"сады", "сад"},
		{"град", "град"},
		{"запад", "запад"},
		{"авент", "авент"},
		{"шиммер", "шиммер"},
		{"энзим", "энзим"},
		{"флизелин", "флизелин"},
		{"флок", "флок"},
		{"муссовый", "муссовый"},
		{"восковый", "восковый"},
		{"шампань", "шампань"},

		// Hyphenated compounds & Latin passthrough
		{"роботов-пылесосов", "робот-пылесос"},
		{"mini-led", "mini-led"},
		{"55C6K", "55c6k"},
	}

	for _, tc := range cases {
		got := lem.Word(tc.word)
		if got != tc.want {
			t.Errorf("Word(%q) = %q, want %q", tc.word, got, tc.want)
		}
	}
}

func TestAdjToNounAndSemanticKey(t *testing.T) {
	lem := loadTestLem(t)
	if st := lem.Stats(); st.AdjNoun < 3500 {
		t.Errorf("expected >=3500 adj_noun pairs, got %d", st.AdjNoun)
	}

	// Positive derivational equivalences
	eqPairs := [][2]string{
		{"зимние кроссовки найк", "кроссовки на зиму найк"},
		{"купить зимние кроссовки", "кроссовки для зимы"},
		{"детский рюкзак", "рюкзак для детей"},
		{"детский рюкзак", "рюкзак для ребенка"},
		{"школьная форма", "форма в школу"},
		{"рыбацкий стул", "стул для рыбалки"},
		{"кожаный ремень", "ремень из кожи"},
		{"серебряное кольцо", "кольцо из серебра"},
	}
	for _, p := range eqPairs {
		k1 := lem.SemanticKey(p[0])
		k2 := lem.SemanticKey(p[1])
		if k1 != k2 || k1 == "" {
			t.Errorf("SemanticKey mismatch: %q -> %q vs %q -> %q", p[0], k1, p[1], k2)
		}
	}

	// Negative guards: semantic drift pairs MUST NOT collide!
	neqPairs := [][2]string{
		{"умные часы", "часы ум"},
		{"мороженая рыба", "мороженое рыба"},
		{"ручной отпариватель", "отпариватель для рук"},
		{"соленые огурцы", "соль огурцы"},
		{"тихий пылесос", "пылесос тишина"},
	}
	for _, p := range neqPairs {
		k1 := lem.SemanticKey(p[0])
		k2 := lem.SemanticKey(p[1])
		if k1 == k2 {
			t.Errorf("SemanticKey false collision: %q and %q both -> %q", p[0], p[1], k1)
		}
	}
}

func TestLemmaKey(t *testing.T) {
	lem := loadTestLem(t)
	k1 := lem.LemmaKey("Купить зимние кроссовки Найк в Москве недорого")
	k2 := lem.LemmaKey("Кроссовки найки зимний Москва")
	if k1 != "зимний кроссовок москва найк" {
		t.Errorf("LemmaKey k1 = %q", k1)
	}
	if k1 != k2 {
		t.Errorf("expected identical keys: %q vs %q", k1, k2)
	}
}

func TestAgreeAdjectiveAndSEOName(t *testing.T) {
	lem := loadTestLem(t)
	if got, ok := lem.AgreeAdjective("синие", "Сумка"); got != "синяя" || !ok {
		t.Errorf("AgreeAdjective(синие, Сумка) = (%q, %v), want (синяя, true)", got, ok)
	}
	if got, ok := lem.AgreeAdjective("женские", "Сумка"); got != "женская" || !ok {
		t.Errorf("AgreeAdjective(женские, Сумка) = (%q, %v), want (женская, true)", got, ok)
	}
	seo, added, agreed := lem.BuildSEOName("Сумка кожаная", "Сумка", []string{"кожаный", "синие", "женские"}, 100)
	if added != 2 || agreed < 2 {
		t.Errorf("BuildSEOName unexpected: seo=%q added=%d agreed=%d", seo, added, agreed)
	}
	t.Logf("BuildSEOName -> %q (added=%d, agreed=%d)", seo, added, agreed)
}

func TestConcurrentSafety(t *testing.T) {
	lem := loadTestLem(t)
	queries := []string{
		"купить кроссовки найк мумия зимние",
		"телевизор tcl 55c6k qd-mini led",
		"русский язык 1 класс учебник канакина",
		"спрей от комаров детский",
		"кофемашина делонги кружка",
		"смартфон сяоми редми ноут 14",
	}
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				q := queries[i%len(queries)]
				if lem.LemmaKey(q) == "" {
					t.Errorf("empty key for %q", q)
				}
			}
		}()
	}
	wg.Wait()
}

func BenchmarkLemmaKey(b *testing.B) {
	lem, err := Load("data")
	if err != nil {
		b.Fatal(err)
	}
	sample := "купить зимние кроссовки найк мужские черные 43 размер"
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = lem.LemmaKey(sample)
	}
}

func TestNormalizePhrase(t *testing.T) {
	// Guard: all 33 single Cyrillic letters must stay themselves (with ё -> е), never verb/advb.
	lem := loadTestLem(t)
	for _, r := range "абвгдеёжзийклмнопрстуфхцчшщъыьэюя" {
		w := string(r)
		want := w
		if r == 'ё' {
			want = "е"
		}
		if got := lem.Word(w); got != want {
			t.Errorf("single-letter Word(%q) = %q, want %q", w, got, want)
		}
	}

	cases := []struct {
		in     string
		keepYo bool
		want   string
	}{
		{"  Купить   Чёрные  кроссовки, 42! ", false, "купить черные кроссовки 42"},
		{"  Купить   Чёрные  кроссовки, 42! ", true, "купить чёрные кроссовки 42"},
		{"Ёлка-иголка (180 см) — хит!", false, "елка-иголка 180 см хит"},
		{"Ёлка-иголка (180 см) — хит!", true, "ёлка-иголка 180 см хит"},
		{"a--b  foo---bar", false, "a b foo bar"},
		{"---  !!!  ...", false, ""},
		{"", false, ""},
	}
	for _, tc := range cases {
		if got := NormalizePhrase(tc.in, tc.keepYo); got != tc.want {
			t.Errorf("NormalizePhrase(%q, %v) = %q, want %q", tc.in, tc.keepYo, got, tc.want)
		}
	}
}

func TestMakeStopWordsAndCustomStop(t *testing.T) {
	lem := loadTestLem(t)
	stop := MakeStopWords([]string{"в", "Москва", "дёшево"})
	if got := lem.LemmaKeyWithStop("Купить кроссовки в Москве дешево", stop); got != "кроссовок купить" {
		t.Errorf("LemmaKeyWithStop = %q, want %q", got, "кроссовок купить")
	}
	if got := lem.LemmaKeyWithStop("Купить в Москве", map[string]struct{}{}); got != "в купить москва" {
		t.Errorf("LemmaKeyWithStop(empty) = %q, want %q", got, "в купить москва")
	}
	if got := lem.SemanticKeyWithStop("Купить зимние кроссовки в Москве", stop); got != "зима кроссовок купить" {
		t.Errorf("SemanticKeyWithStop = %q, want %q", got, "зима кроссовок купить")
	}
	// GeneralKeyStopWords preserves commercial nouns like "магазин"
	if got := lem.SemanticKeyWithStop("оквэд для цветочного магазина", GeneralKeyStopWords); got != "магазин оквэд цветок" {
		t.Errorf("SemanticKeyWithStop(GeneralKeyStopWords) = %q, want %q", got, "магазин оквэд цветок")
	}
	// DefaultKeyStopWords preserves negation marker "без"
	if k1, k2 := lem.SemanticKey("шоколад без сахара"), lem.SemanticKey("шоколад с сахаром"); k1 == k2 || !strings.Contains(k1, "без") {
		t.Errorf("DefaultKeyStopWords must preserve 'без': got %q vs %q", k1, k2)
	}
}

func TestLookupWordAndLemmatizeAll(t *testing.T) {
	lem := loadTestLem(t)
	if m := lem.Manifest(); m == nil || m.DictVersion == "" {
		t.Errorf("expected loaded Manifest with DictVersion, got %+v", m)
	}

	lookupCases := []struct {
		word    string
		wantLem string
		wantSrc string
	}{
		{"духи", "духи", SourceOverride},
		{"кроссовки", "кроссовок", SourceDict},
		{"судебного", "судебный", SourceRule},
		{"интернет-магазинов", "интернет-магазин", SourceRule},
		{"abrakadabra123", "abrakadabra123", SourceOOV},
	}
	for _, tc := range lookupCases {
		gotL, gotS := lem.LookupWord(tc.word)
		if gotL != tc.wantLem || gotS != tc.wantSrc {
			t.Errorf("LookupWord(%q) = (%q, %q), want (%q, %q)", tc.word, gotL, gotS, tc.wantLem, tc.wantSrc)
		}
	}

	// Verify single-pass LemmatizeAllWithStop matches individual methods identically
	phrases := []string{
		"Купить зимние кроссовки Найк в Москве",
		"Шоколад без сахара и без глютена",
		"Доставка в Ростове-на-Дону",
		"",
	}
	for _, p := range phrases {
		txt, key, sem := lem.LemmatizeAll(p)
		if txt != lem.LemmatizeString(p) || key != lem.LemmaKey(p) || sem != lem.SemanticKey(p) {
			t.Errorf("LemmatizeAll(%q) = (%q, %q, %q), mismatch with individual methods", p, txt, key, sem)
		}
		txt2, key2, sem2 := lem.LemmatizeAllWithStop(p, GeneralKeyStopWords)
		if txt2 != lem.LemmatizeString(p) || key2 != lem.LemmaKeyWithStop(p, GeneralKeyStopWords) || sem2 != lem.SemanticKeyWithStop(p, GeneralKeyStopWords) {
			t.Errorf("LemmatizeAllWithStop(%q) mismatch with individual methods", p)
		}
	}
}

func TestLoadManifest(t *testing.T) {
	m, err := LoadManifest("data")
	if err != nil {
		t.Fatalf("LoadManifest(data): %v", err)
	}
	if m.SchemaVersion != DictSchemaVersion {
		t.Errorf("SchemaVersion = %d, want %d", m.SchemaVersion, DictSchemaVersion)
	}
	if m.EngineMinVersion == "" || m.DictVersion == "" {
		t.Errorf("missing versions in manifest: %+v", m)
	}
	for _, k := range []string{"lemmas", "adj_forms", "types", "adj_noun", "overrides"} {
		if m.Counts[k] <= 0 {
			t.Errorf("manifest counts[%q] = %d", k, m.Counts[k])
		}
		fname := m.Files[k]
		if len(m.SHA256[fname]) != 64 {
			t.Errorf("manifest sha256[%q] invalid: %q", fname, m.SHA256[fname])
		}
	}
}

func TestGoldenLemmas(t *testing.T) {
	lem := loadTestLem(t)
	f, err := os.Open(filepath.Join("tests", "golden", "golden_lemmas.tsv"))
	if err != nil {
		t.Fatalf("open golden_lemmas.tsv: %v", err)
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	count := 0
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) < 2 {
			continue
		}
		count++
		if got := lem.Word(parts[0]); got != parts[1] {
			t.Errorf("golden Word(%q) = %q, want %q", parts[0], got, parts[1])
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	if count < 500 {
		t.Errorf("expected >=500 golden lemmas, got %d", count)
	}
}

func TestGoldenEcommerce(t *testing.T) {
	lem := loadTestLem(t)
	f, err := os.Open(filepath.Join("tests", "golden", "golden_ecommerce.tsv"))
	if err != nil {
		t.Fatalf("open golden_ecommerce.tsv: %v", err)
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	count := 0
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) < 4 {
			continue
		}
		count++
		kind, arg1, arg2, want := parts[0], parts[1], parts[2], parts[3]
		switch kind {
		case "lemma_key":
			if got := lem.LemmaKey(arg1); got != want {
				t.Errorf("golden LemmaKey(%q) = %q, want %q", arg1, got, want)
			}
		case "semantic_key_eq":
			k1, k2 := lem.SemanticKey(arg1), lem.SemanticKey(arg2)
			if k1 != want || k2 != want {
				t.Errorf("golden SemanticKey(%q)=%q, (%q)=%q, want %q", arg1, k1, arg2, k2, want)
			}
		case "semantic_key_neq":
			k1, k2 := lem.SemanticKey(arg1), lem.SemanticKey(arg2)
			if k1 == k2 {
				t.Errorf("golden SemanticKey false collision: %q and %q both -> %q", arg1, arg2, k1)
			}
		case "agree_adjective":
			if got, _ := lem.AgreeAdjective(arg1, arg2); got != want {
				t.Errorf("golden AgreeAdjective(%q, %q) = %q, want %q", arg1, arg2, got, want)
			}
		case "build_seo_name":
			meta := strings.Split(arg1, "|")
			maxLen, _ := strconv.Atoi(meta[2])
			kws := strings.Split(arg2, "|")
			if got, _, _ := lem.BuildSEOName(meta[0], meta[1], kws, maxLen); got != want {
				t.Errorf("golden BuildSEOName(%q, %q, %v) = %q, want %q", meta[0], meta[1], kws, got, want)
			}
		default:
			t.Errorf("unknown golden kind: %q", kind)
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	if count < 100 {
		t.Errorf("expected >=100 golden ecommerce cases, got %d", count)
	}
}
