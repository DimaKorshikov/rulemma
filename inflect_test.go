package rulemma_test

import (
	"testing"

	"github.com/DimaKorshikov/rulemma"
)

func TestInflectSingleWordsAndGuards(t *testing.T) {
	lem := mustLoadTest(t)

	cases := []struct {
		word string
		cs   string
		num  string
		want string
	}{
		{"машина", "accs", "sing", "машину"},
		{"машина", "gent", "sing", "машины"},
		{"машина", "gent", "plur", "машин"},
		{"край", "loct", "sing", "крае"},
		{"Москва", "loct", "sing", "Москве"},
		{"кроссовок", "gent", "plur", "кроссовок"},
		{"кроссовки", "gent", "", "кроссовок"},
		{"товар", "gent", "sing", "товара"},
		{"товар", "gent", "plur", "товаров"},
		{"ребенок", "gent", "plur", "детей"},
		// Pltm protection (never fabricate singular!)
		{"ножницы", "datv", "sing", "ножницам"},
		{"ножницы", "gent", "", "ножниц"},
		{"духи", "gent", "sing", "духов"},
		// Sgtm protection (fall back safely on plur)
		{"оборудование", "gent", "sing", "оборудования"},
		{"молоко", "gent", "plur", "молока"},
		// Fixd protection (indeclinable)
		{"пальто", "loct", "sing", "пальто"},
		{"кофе", "gent", "sing", "кофе"},
		{"метро", "loct", "sing", "метро"},
		// Case aliases
		{"сумка", "в", "ед", "сумку"},
		{"сумка", "р", "мн", "сумок"},
	}
	for _, tc := range cases {
		got, ok := lem.Inflect(tc.word, tc.cs, tc.num)
		if !ok || got != tc.want {
			t.Errorf("Inflect(%q, %q, %q) = (%q, %v), want (%q, true)", tc.word, tc.cs, tc.num, got, ok, tc.want)
		}
	}

	// Non-Cyrillic brands & SKU codes must not be mangled
	for _, raw := range []string{"LG", "F2V5", "Nike", "S24", "100x100"} {
		got, ok := lem.Inflect(raw, "gent", "")
		if ok || got != raw {
			t.Errorf("Inflect(%q) should return untouched (%q, false), got (%q, %v)", raw, raw, got, ok)
		}
	}
}

func TestInflectPhrase(t *testing.T) {
	lem := mustLoadTest(t)

	cases := []struct {
		phrase string
		cs     string
		num    string
		want   string
	}{
		{"стиральная машина LG F2V5", "accs", "", "стиральную машину LG F2V5"},
		{"Краснодарский край", "loct", "", "Краснодарском крае"},
		{"зимние кроссовки Nike Air", "gent", "", "зимних кроссовок Nike Air"},
		{"ножницы портновские", "datv", "", "ножницам портновским"},
		{"офисное оборудование", "gent", "", "офисного оборудования"},
		{"чехол для телефона", "gent", "", "чехла для телефона"},
		{"крем от морщин", "accs", "", "крем от морщин"},
		{"фильтр воды", "gent", "", "фильтра воды"},
		{"теплое пальто Zara", "loct", "", "теплом пальто Zara"},
	}
	for _, tc := range cases {
		got, ok := lem.InflectPhrase(tc.phrase, tc.cs, tc.num)
		if !ok || got != tc.want {
			t.Errorf("InflectPhrase(%q, %q, %q) = (%q, %v), want (%q, true)", tc.phrase, tc.cs, tc.num, got, ok, tc.want)
		}
	}
}

func TestAllFormsAgreeNumberAndPreposition(t *testing.T) {
	lem := mustLoadTest(t)

	forms := lem.AllForms("сумка")
	if len(forms) < 6 {
		t.Fatalf("AllForms(сумка) too short: %v", forms)
	}
	has := map[string]bool{}
	for _, f := range forms {
		has[f] = true
	}
	for _, req := range []string{"сумка", "сумки", "сумке", "сумку", "сумкой", "сумок", "сумкам"} {
		if !has[req] {
			t.Errorf("AllForms(сумка) missing %q in %v", req, forms)
		}
	}

	agreeCases := []struct {
		n    float64
		word string
		want string
	}{
		{1, "товар", "1 товар"},
		{2, "товар", "2 товара"},
		{5, "товар", "5 товаров"},
		{11, "товар", "11 товаров"},
		{14, "товар", "14 товаров"},
		{21, "товар", "21 товар"},
		{22, "товар", "22 товара"},
		{1.5, "товар", "1.5 товара"},
		{0.5, "литр", "0.5 литра"},
		{0, "рубль", "0 рублей"},
		{5, "ножницы", "5 ножниц"},
		{21, "ножницы", "21 ножницы"},
		{3, "пальто", "3 пальто"},
	}
	for _, tc := range agreeCases {
		if got := lem.AgreeNumber(tc.n, tc.word); got != tc.want {
			t.Errorf("AgreeNumber(%v, %q) = %q, want %q", tc.n, tc.word, got, tc.want)
		}
	}

	prepCases := [][3]string{
		{"в", "Москве", "в"},
		{"в", "вторник", "во"},
		{"в", "Франции", "во"},
		{"в", "сне", "во"},
		{"В", "вторник", "Во"},
		{"с", "молоком", "с"},
		{"с", "скидкой", "со"},
		{"с", "склада", "со"},
		{"с", "мной", "со"},
		{"к", "дому", "к"},
		{"к", "всем", "ко"},
		{"к", "мне", "ко"},
		{"о", "товаре", "о"},
		{"о", "акциях", "об"},
		{"о", "установке", "об"},
		{"о", "яблоках", "о"},
		{"о", "вселенной", "о"},
		{"о", "всем", "обо"},
		{"о", "мне", "обо"},
	}
	for _, tc := range prepCases {
		if got := rulemma.Preposition(tc[0], tc[1]); got != tc[2] {
			t.Errorf("Preposition(%q, %q) = %q, want %q", tc[0], tc[1], got, tc[2])
		}
	}
}
