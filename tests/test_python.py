"""Unit and golden regression tests for the rulemma Python package (native CGO & CLI fallback)."""
from __future__ import annotations

import tempfile
import time
import unittest
from pathlib import Path

from rulemma import DEFAULT_STOP_WORDS, RuLemma, __version__, normalize_phrase

ROOT = Path(__file__).resolve().parents[1]
GOLDEN_DIR = ROOT / "tests" / "golden"


class TestRuLemmaPython(unittest.TestCase):
    @classmethod
    def setUpClass(cls) -> None:
        cls.lem = RuLemma(workers=4)

    @classmethod
    def tearDownClass(cls) -> None:
        cls.lem.close()

    def test_backend_version_and_abi(self) -> None:
        self.assertEqual(self.lem.backend, "native")
        self.assertEqual(__version__, "0.2.2")
        self.assertEqual(self.lem.abi_version, 1)
        self.assertEqual(self.lem.library_version, "0.2.2")
        st = self.lem.stats()
        self.assertGreaterEqual(st["lemmas"], 150_000)
        self.assertGreaterEqual(st["adj_noun"], 3_500)
        self.assertGreaterEqual(st["overrides"], 300)

    def test_single_word_and_v021_fixes(self) -> None:
        cases = {
            "кроссовки": "кроссовок",
            "спрей": "спрей",
            "сяоми": "сяоми",
            "хуавей": "хуавей",
            "хонор": "хонор",
            "кофемашина": "кофемашина",
            "кружка": "кружка",
            "сад": "сад",
            "сады": "сад",
            "авент": "авент",
            "шиммер": "шиммер",
            "энзим": "энзим",
            "флок": "флок",
            "муссовый": "муссовый",
            "мороженое": "мороженое",
            "бодиметрии": "бодиметрия",
            "комаров": "комар",
            "роботов-пылесосов": "робот-пылесос",
            # v0.2.1 fixes
            "отпариватели": "отпариватель",
            "дочерей": "дочь",
            "воробьев": "воробей",
            "автодорог": "автодорога",
            "большие": "большой",
            "см": "см",
            "мм": "мм",
            "кг": "кг",
            "квт": "квт",
            "мл": "мл",
            "шт": "шт",
            "е": "е",
            "д": "д",
            "т": "т",
            "мелки": "мелок",
            "полка": "полка",
            "оцинкованные": "оцинкованный",
            "встраиваемые": "встраиваемый",
            "развивающие": "развивающий",
            "плавающие": "плавающий",
            "обслуживаемые": "обслуживаемый",
            "орнаментированные": "орнаментированный",
            "оксидированные": "оксидированный",
        }
        for w, want in cases.items():
            self.assertEqual(self.lem.word(w), want, f"word({w!r})")

    def test_normalize_phrase(self) -> None:
        raw = "  Купить   Чёрные  кроссовки, 42 см! "
        self.assertEqual(normalize_phrase(raw), "купить черные кроссовки 42 см")
        self.assertEqual(normalize_phrase(raw, keep_yo=True), "купить чёрные кроссовки 42 см")
        self.assertEqual(self.lem.normalize_phrase(raw), "купить черные кроссовки 42 см")
        self.assertEqual(self.lem.normalize_phrase(raw, keep_yo=True), "купить чёрные кроссовки 42 см")
        self.assertEqual(
            self.lem.normalize_batch([raw, "Ёлка-иголка (180 см)"]),
            ["купить черные кроссовки 42 см", "елка-иголка 180 см"],
        )
        self.assertEqual(
            self.lem.normalize_batch([raw, "Ёлка-иголка (180 см)"], keep_yo=True),
            ["купить чёрные кроссовки 42 см", "ёлка-иголка 180 см"],
        )

    def test_lemma_key_semantic_key_and_custom_stopwords(self) -> None:
        self.assertEqual(
            self.lem.lemma_key("Купить зимние кроссовки Найк"),
            self.lem.lemma_key("кроссовки найки зимний"),
        )
        # Custom stopwords (single + batch + empty stop set)
        self.assertIn("купить", DEFAULT_STOP_WORDS)
        self.assertEqual(
            self.lem.lemma_key("Купить кроссовки в Москве", stopwords={"в", "москва"}),
            "кроссовок купить",
        )
        self.assertEqual(
            self.lem.lemma_key("Купить в Москве", stopwords=[]),
            "в купить москва",
        )
        self.assertEqual(
            self.lem.semantic_key("Купить зимние кроссовки в Москве", stopwords={"в", "москва"}),
            "зима кроссовок купить",
        )
        batch = ["Купить зимние кроссовки в Москве", "Кроссовки на зиму в Москве"]
        self.assertEqual(
            self.lem.lemma_keys(batch, stopwords={"в", "москва"}),
            ["зимний кроссовок купить", "зима кроссовок на"],
        )
        self.assertEqual(
            self.lem.semantic_keys(batch, stopwords={"в", "на", "москва", "купить"}),
            ["зима кроссовок", "зима кроссовок"],
        )
        pairs = self.lem.lemmatize_and_keys(batch, stopwords={"в", "москва"})
        self.assertEqual(pairs[0][1], "зимний кроссовок купить")

    def test_cli_fallback_custom_stopwords_and_tsv(self) -> None:
        with RuLemma(workers=2, prefer_native=False) as cli_lem:
            self.assertEqual(cli_lem.backend, "cli")
            self.assertEqual(
                cli_lem.lemma_key("Купить кроссовки в Москве", stopwords={"в", "москва"}),
                "кроссовок купить",
            )
            self.assertEqual(
                cli_lem.lemma_key("Купить в Москве", stopwords=[]),
                "в купить москва",
            )
            self.assertEqual(
                cli_lem.semantic_key("зимние кроссовки", stopwords=set()),
                "зима кроссовок",
            )

            with tempfile.TemporaryDirectory() as td:
                in_p = Path(td) / "in.tsv"
                out_p = Path(td) / "out.tsv"
                in_p.write_text("query\tcnt\nзимние кроссовки в Москве\t10\n", encoding="utf-8")
                cli_lem.lemmatize_tsv(in_p, out_p, col=0, mode="all", header=True, stopwords="в,москва")
                lines = out_p.read_text(encoding="utf-8").splitlines()
                self.assertEqual(lines[0], "query\tcnt\tlemma_text\tlemma_key\tsemantic_key")
                self.assertTrue(lines[1].endswith("\tзимний кроссовок\tзима кроссовок"))

    def test_adj_to_noun_and_seo_name(self) -> None:
        noun, ok = self.lem.adj_to_noun("зимние")
        self.assertTrue(ok)
        self.assertEqual(noun, "зима")

        agreed, changed = self.lem.agree_adjective("синие", "Сумка")
        self.assertTrue(changed)
        self.assertEqual(agreed, "синяя")

        agreed_p, changed_p = self.lem.agree_adjective("оцинкованные", "Ведро")
        self.assertTrue(changed_p)
        self.assertEqual(agreed_p, "оцинкованное")

        seo, added, n_agreed = self.lem.build_seo_name("Сумка кожаная", "Сумка", ["кожаный", "синие", "женские"], 100)
        self.assertEqual(added, 2)
        self.assertGreaterEqual(n_agreed, 2)
        self.assertIn("синяя", seo)
        self.assertIn("женская", seo)

    def test_golden_lemmas(self) -> None:
        path = GOLDEN_DIR / "golden_lemmas.tsv"
        count = 0
        for line in path.read_text(encoding="utf-8").splitlines():
            s = line.strip()
            if not s or s.startswith("#"):
                continue
            word, want = s.split("\t")[:2]
            count += 1
            self.assertEqual(self.lem.word(word), want, f"golden_lemmas: {word!r}")
        self.assertGreaterEqual(count, 500)

    def test_golden_ecommerce(self) -> None:
        path = GOLDEN_DIR / "golden_ecommerce.tsv"
        count = 0
        for line in path.read_text(encoding="utf-8").splitlines():
            s = line.strip()
            if not s or s.startswith("#"):
                continue
            kind, arg1, arg2, want = s.split("\t")[:4]
            count += 1
            if kind == "lemma_key":
                self.assertEqual(self.lem.lemma_key(arg1), want, f"lemma_key({arg1!r})")
            elif kind == "semantic_key_eq":
                self.assertEqual(self.lem.semantic_key(arg1), want, f"semantic_key({arg1!r})")
                self.assertEqual(self.lem.semantic_key(arg2), want, f"semantic_key({arg2!r})")
            elif kind == "semantic_key_neq":
                self.assertNotEqual(self.lem.semantic_key(arg1), self.lem.semantic_key(arg2), f"neq({arg1!r}, {arg2!r})")
            elif kind == "agree_adjective":
                got, _ = self.lem.agree_adjective(arg1, arg2)
                self.assertEqual(got, want, f"agree_adjective({arg1!r}, {arg2!r})")
            elif kind == "build_seo_name":
                base, ptype, maxlen = arg1.split("|")
                kws = arg2.split("|")
                got, _, _ = self.lem.build_seo_name(base, ptype, kws, int(maxlen))
                self.assertEqual(got, want, f"build_seo_name({base!r}, {ptype!r})")
        self.assertGreaterEqual(count, 100)

    def test_batch_and_latency(self) -> None:
        batch = ["зимние кроссовки найк", "кроссовки на зиму найк", "спрей от комаров детский", "кофемашина хуавей"] * 2500
        t0 = time.perf_counter()
        keys = self.lem.semantic_keys(batch)
        dt = time.perf_counter() - t0
        self.assertEqual(len(keys), len(batch))
        self.assertEqual(keys[0], keys[1])
        self.assertLess(dt, 1.0, f"10k native batch took too long: {dt:.3f}s")


if __name__ == "__main__":
    unittest.main()
