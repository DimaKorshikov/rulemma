package rulemma

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
)

func TestFastLowerNoYoAndNormalization(t *testing.T) {
	cases := []struct {
		in       string
		fastNoYo bool
		wantNoYo string
		wantLow  string
	}{
		{"кофемашина", true, "кофемашина", "кофемашина"},
		{"робот-пылесос", true, "робот-пылесос", "робот-пылесос"},
		{"iphone15", true, "iphone15", "iphone15"},
		{"ежик", true, "ежик", "ежик"},
		{"ёжик", false, "ежик", "ёжик"},
		{"Ёжик", false, "ежик", "ёжик"},
		{"Кофемашина", false, "кофемашина", "кофемашина"},
		{"АЯая", false, "аяая", "аяая"},
		{"Sony", false, "sony", "sony"},
	}
	var scratch []byte
	for _, tc := range cases {
		if got := isFastLowerNoYo([]byte(tc.in)); got != tc.fastNoYo {
			t.Errorf("isFastLowerNoYo(%q) = %v, want %v", tc.in, got, tc.fastNoYo)
		}
		if got := string(normalizeLowerNoYoBytes([]byte(tc.in), scratch)); got != tc.wantNoYo {
			t.Errorf("normalizeLowerNoYoBytes(%q) = %q, want %q", tc.in, got, tc.wantNoYo)
		}
		if got := string(normalizeLowerBytes([]byte(tc.in), scratch)); got != tc.wantLow {
			t.Errorf("normalizeLowerBytes(%q) = %q, want %q", tc.in, got, tc.wantLow)
		}
	}
}

func TestCompactStrMapGrowthOverwriteAndOverflow(t *testing.T) {
	m := newCompactStrMap(4, 32)
	for i := 0; i < 200; i++ {
		k := fmt.Sprintf("слово_%d", i)
		v := fmt.Sprintf("лемма_%d", i/3)
		m.putString(k, v, true)
	}
	// Identity + overwrite
	m.putString("слово_5", "переопределено", false)
	m.putString("самосебе", "самосебе", true)
	longKey := strings.Repeat("а", 200) // 400 UTF-8 bytes (> 255 -> overflow map)
	m.putString(longKey, "длинная", false)
	m.freeze()

	if m.Len() != 202 {
		t.Fatalf("Len() = %d, want 202", m.Len())
	}
	if got, ok := m.Get("слово_5"); !ok || got != "переопределено" {
		t.Fatalf("Get(слово_5) = (%q, %v), want переопределено", got, ok)
	}
	if got, ok := m.Get("самосебе"); !ok || got != "самосебе" {
		t.Fatalf("Get(самосебе) = (%q, %v), want самосебе", got, ok)
	}
	if got, ok := m.Get(longKey); !ok || got != "длинная" {
		t.Fatalf("Get(longKey) = (%q, %v), want длинная", got, ok)
	}
	if _, ok := m.Get("отсутствует"); ok {
		t.Fatalf("expected missing key to return false")
	}
}

func TestCompactU32MapAndAdjFormsTable(t *testing.T) {
	u := newCompactU32Map(4, 32)
	for i := 0; i < 150; i++ {
		u.putBytes([]byte(fmt.Sprintf("токен_%d", i)), uint32(i*17+3))
	}
	u.putBytes([]byte("токен_10"), 0xDEADBEEF)
	u.freeze()
	if u.Len() != 150 {
		t.Fatalf("u.Len() = %d, want 150", u.Len())
	}
	if v, ok := u.Get("токен_10"); !ok || v != 0xDEADBEEF {
		t.Fatalf("u.Get(токен_10) = (%x, %v)", v, ok)
	}

	af := newAdjFormsTable(4, 64)
	af.putComposite([]byte("зимний|masc|sing"), []byte("зимний"))
	af.putComposite([]byte("зимний|femn|sing"), []byte("зимняя"))
	af.putComposite([]byte("зимний|neut|sing"), []byte("зимнее"))
	af.putComposite([]byte("зимний||plur"), []byte("зимние"))
	af.putComposite([]byte("особый|custom|dual"), []byte("особая_форма"))
	af.freeze()

	if af.Len() != 5 {
		t.Fatalf("af.Len() = %d, want 5", af.Len())
	}
	if got, ok := af.Get("зимний", "femn", "sing"); !ok || got != "зимняя" {
		t.Fatalf("af.Get(зимний, femn, sing) = (%q, %v)", got, ok)
	}
	if got, ok := af.Get("зимний", "", "plur"); !ok || got != "зимние" {
		t.Fatalf("af.Get(зимний, '', plur) = (%q, %v)", got, ok)
	}
	if got, ok := af.Get("особый", "custom", "dual"); !ok || got != "особая_форма" {
		t.Fatalf("af.Get(особый, custom, dual) = (%q, %v)", got, ok)
	}
}

func TestMemoryFSAndOnDemandLoadTableConcurrent(t *testing.T) {
	readFile := func(name string) []byte {
		b, err := os.ReadFile("data/" + name)
		if err != nil {
			t.Fatalf("read data/%s: %v", name, err)
		}
		return b
	}

	// Start with minimal core-only tables (like thin WASM first-paint bundle).
	lem, err := LoadMemory(map[string][]byte{
		"lemmas.tsv.gz":   readFile("lemmas.tsv.gz"),
		"overrides.tsv":   readFile("overrides.tsv"),
		"adj_noun.tsv.gz": readFile("adj_noun.tsv.gz"),
	}, Options{})
	if err != nil {
		t.Fatalf("LoadMemory core: %v", err)
	}

	if got := lem.Word("кофемашины"); got != "кофемашина" {
		t.Fatalf("core Word(кофемашины) = %q", got)
	}
	if got := lem.SemanticKey("зимние кроссовки"); got != "зима кроссовок" {
		t.Fatalf("core SemanticKey = %q", got)
	}
	if st := lem.Stats(); st.AdjForms != 0 || st.Morph != 0 || st.ParadigmWords != 0 {
		t.Fatalf("expected optional tables unloaded initially, got %+v", st)
	}
	if info := lem.Analyze("пил"); info.Known {
		t.Fatalf("expected Analyze.Known=false before morph load")
	}

	// Concurrently read while lazily loading optional tables on demand (verified under -race).
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_ = lem.Word("кроссовки")
					_ = lem.Analyze("пил")
					_, _ = lem.Inflect("машина", "gent", "")
					_, _ = lem.AgreeAdjective("зимний", "Куртка")
				}
			}
		}()
	}

	for _, tbl := range []string{"types.tsv.gz", "adj_forms.tsv.gz", "morph.tsv.gz", "alt_lemmas.tsv.gz", "paradigms.tsv.gz"} {
		if err := lem.LoadTable(tbl, readFile(tbl)); err != nil {
			close(stop)
			wg.Wait()
			t.Fatalf("LoadTable(%s): %v", tbl, err)
		}
	}
	close(stop)
	wg.Wait()

	if info := lem.Analyze("пил"); !info.Known || !info.IsHomonym {
		t.Fatalf("expected Analyze(пил) known homonym after LoadTable, got %+v", info)
	}
	if inf, ok := lem.Inflect("машина", "gent", ""); !ok || inf != "машины" {
		t.Fatalf("expected Inflect(машина, gent) = машины after LoadTable, got (%q, %v)", inf, ok)
	}
	if agreed, ok := lem.AgreeAdjective("зимний", "Куртка"); !ok || agreed != "зимняя" {
		t.Fatalf("expected AgreeAdjective after LoadTable = зимняя, got (%q, %v)", agreed, ok)
	}
	if err := lem.LoadTable("unknown.tsv.gz", []byte("a\tb\n")); err == nil {
		t.Fatalf("expected error for unsupported table name")
	}
}
