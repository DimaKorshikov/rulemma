package embedded_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/DimaKorshikov/rulemma"
	"github.com/DimaKorshikov/rulemma/embedded"
)

func TestEmbeddedLoadAndManifest(t *testing.T) {
	lem, err := embedded.Load()
	if err != nil {
		t.Fatalf("embedded.Load(): %v", err)
	}
	diskLem, err := rulemma.Load(filepath.Join("..", "data"))
	if err != nil {
		t.Fatalf("rulemma.Load(../data): %v", err)
	}

	if lem.Stats() != diskLem.Stats() {
		t.Errorf("embedded stats %+v != disk stats %+v", lem.Stats(), diskLem.Stats())
	}

	for _, w := range []string{"спрей", "см", "отпариватели", "оцинкованные", "мелки", "роботов-пылесосов"} {
		if got, want := lem.Word(w), diskLem.Word(w); got != want {
			t.Errorf("Word(%q): embedded=%q != disk=%q", w, got, want)
		}
	}

	m, err := embedded.Manifest()
	if err != nil {
		t.Fatalf("embedded.Manifest(): %v", err)
	}
	if m.SchemaVersion != rulemma.DictSchemaVersion {
		t.Errorf("SchemaVersion = %d, want %d", m.SchemaVersion, rulemma.DictSchemaVersion)
	}
	if m.Counts["lemmas"] < 150000 || m.Counts["overrides"] < 300 {
		t.Errorf("unexpected manifest counts: %+v", m.Counts)
	}
}

func TestEmbeddedWithExtraOverrides(t *testing.T) {
	tmp := t.TempDir()
	ovPath := filepath.Join(tmp, "custom.tsv")
	if err := os.WriteFile(ovPath, []byte("квадрокоптеры\tдрон\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	lem, err := embedded.LoadWithOptions(rulemma.Options{ExtraOverridesPath: ovPath})
	if err != nil {
		t.Fatalf("embedded.LoadWithOptions: %v", err)
	}
	if got := lem.Word("квадрокоптеры"); got != "дрон" {
		t.Errorf("Word(квадрокоптеры) = %q, want %q", got, "дрон")
	}
}
