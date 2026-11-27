package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func buildTestCLI(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "rulemma-test")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, out)
	}
	return bin
}

func TestCLIVersionInfoLemmatizeAndTSV(t *testing.T) {
	bin := buildTestCLI(t)
	dataDir := filepath.Join("..", "..", "data")

	// 1. version
	out, err := exec.Command(bin, "version").CombinedOutput()
	if err != nil {
		t.Fatalf("version failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "0.2.2") || !strings.Contains(string(out), "abi=1") {
		t.Errorf("unexpected version output: %q", string(out))
	}

	// 2. info
	out, err = exec.Command(bin, "info", "--data", dataDir).CombinedOutput()
	if err != nil {
		t.Fatalf("info failed: %v\n%s", err, out)
	}
	for _, want := range []string{"version:", "lemmas:", "overrides:", "dict_ver:"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("info output missing %q:\n%s", want, out)
		}
	}

	// 3. lemmatize --mode key --stopwords
	cmd := exec.Command(bin, "lemmatize", "--data", dataDir, "--mode", "key", "--stopwords", "в,москва")
	cmd.Stdin = strings.NewReader("Купить кроссовки в Москве\n")
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("lemmatize --stopwords failed: %v\n%s", err, out)
	}
	if got := strings.TrimSpace(string(out)); got != "кроссовок купить" {
		t.Errorf("lemmatize --stopwords got %q, want %q", got, "кроссовок купить")
	}

	// 4. lemmatize --mode key --stopwords none
	cmd = exec.Command(bin, "lemmatize", "--data", dataDir, "--mode", "key", "--stopwords", "none")
	cmd.Stdin = strings.NewReader("Купить в Москве\n")
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("lemmatize --stopwords none failed: %v\n%s", err, out)
	}
	if got := strings.TrimSpace(string(out)); got != "в купить москва" {
		t.Errorf("lemmatize --stopwords none got %q, want %q", got, "в купить москва")
	}

	// 5. lemmatize --mode norm with and without --keep-yo
	cmd = exec.Command(bin, "lemmatize", "--mode", "norm")
	cmd.Stdin = strings.NewReader("  Чёрные   кроссовки, 42! \n")
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("lemmatize --mode norm failed: %v\n%s", err, out)
	}
	if got := strings.TrimSpace(string(out)); got != "черные кроссовки 42" {
		t.Errorf("norm got %q", got)
	}

	cmd = exec.Command(bin, "lemmatize", "--mode", "norm", "--keep-yo")
	cmd.Stdin = strings.NewReader("  Чёрные   кроссовки, 42! \n")
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("lemmatize --mode norm --keep-yo failed: %v\n%s", err, out)
	}
	if got := strings.TrimSpace(string(out)); got != "чёрные кроссовки 42" {
		t.Errorf("norm --keep-yo got %q", got)
	}

	// 6. tsv --header across all modes (fixes v0.2.0 header bug for semkey/all)
	tmp := t.TempDir()
	inTSV := filepath.Join(tmp, "in.tsv")
	if err := os.WriteFile(inTSV, []byte("query\tvol\nзимние кроссовки\t100\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	modeHeaders := map[string]string{
		"text":   "query\tvol\tlemma_text",
		"key":    "query\tvol\tlemma_key",
		"semkey": "query\tvol\tsemantic_key",
		"both":   "query\tvol\tlemma_text\tlemma_key",
		"all":    "query\tvol\tlemma_text\tlemma_key\tsemantic_key",
		"norm":   "query\tvol\tnorm_phrase",
	}
	for mode, wantHdr := range modeHeaders {
		outTSV := filepath.Join(tmp, "out_"+mode+".tsv")
		cmd := exec.Command(bin, "tsv", "--data", dataDir, "--in", inTSV, "--out", outTSV, "--col", "0", "--mode", mode, "--header")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("tsv mode=%s failed: %v\n%s", mode, err, out)
		}
		b, err := os.ReadFile(outTSV)
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(strings.TrimSpace(string(b)), "\n")
		if len(lines) != 2 {
			t.Fatalf("tsv mode=%s expected 2 lines, got %d", mode, len(lines))
		}
		if lines[0] != wantHdr {
			t.Errorf("tsv mode=%s header = %q, want %q", mode, lines[0], wantHdr)
		}
	}
}
