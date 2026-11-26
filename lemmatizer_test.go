package rulemma

import (
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
