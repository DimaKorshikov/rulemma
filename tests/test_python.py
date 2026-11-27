"""Unit and golden regression tests for the rulemma Python package (native CGO & CLI fallback)."""
from __future__ import annotations

import os
import subprocess
import sys
import tempfile
import time
import unittest
from pathlib import Path

from rulemma import (
    DEFAULT_STOP_WORDS,
    ECOMMERCE_STOP_WORDS,
    GENERAL_STOP_WORDS,
    RuLemma,
    __version__,
    normalize_homoglyphs,
    normalize_phrase,
    pluralize_form,
    preposition,
)

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
        self.assertEqual(__version__, "0.3.0")
        self.assertEqual(self.lem.abi_version, 1)
        self.assertEqual(self.lem.library_version, __version__)
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

    def test_v023_api_and_profiles(self) -> None:
        # Parity between Go Tokenize and Python regex on consecutive hyphens
        self.assertEqual(normalize_phrase("a--b foo---bar"), "a b foo bar")
        self.assertEqual(self.lem.normalize_phrase("a--b foo---bar"), "a b foo bar")

        # Stop-word profiles & negation safety
        self.assertNotIn("без", DEFAULT_STOP_WORDS)
        self.assertNotIn("без", GENERAL_STOP_WORDS)
        self.assertNotIn("магазин", GENERAL_STOP_WORDS)
        self.assertIn("магазин", ECOMMERCE_STOP_WORDS)
        self.assertEqual(
            self.lem.semantic_key("оквэд для цветочного магазина", stopwords=GENERAL_STOP_WORDS),
            "магазин оквэд цветок",
        )
        self.assertNotEqual(
            self.lem.semantic_key("шоколад без сахара"),
            self.lem.semantic_key("шоколад с сахаром"),
        )

        # lookup_word & is_known
        self.assertEqual(self.lem.lookup_word("духи"), ("духи", "override"))
        self.assertEqual(self.lem.lookup_word("кроссовки"), ("кроссовок", "dict"))
        self.assertEqual(self.lem.lookup_word("судебного"), ("судебный", "rule"))
        self.assertEqual(self.lem.lookup_word("abrakadabra123"), ("abrakadabra123", "oov"))
        self.assertTrue(self.lem.is_known("кроссовки"))
        self.assertFalse(self.lem.is_known("судебного"))

        # lemmatize_all parity (native vs CLI, default & custom stopwords)
        items = ["зимние ботинки найк", "шоколад без сахара", ""]
        all_native = self.lem.lemmatize_all(items)
        self.assertEqual(len(all_native), 3)
        self.assertEqual(all_native[0], ("зимний ботинок найк", "ботинок зимний найк", "ботинок зима найк"))
        self.assertEqual(all_native[1], ("шоколад без сахар", "без сахар шоколад", "без сахар шоколад"))
        self.assertEqual(all_native[2], ("", "", ""))
        all_gen = self.lem.lemmatize_all(["оквэд для цветочного магазина"], stopwords=GENERAL_STOP_WORDS)
        self.assertEqual(all_gen[0][2], "магазин оквэд цветок")
        with RuLemma(prefer_native=False) as cli_lem:
            self.assertEqual(cli_lem.lemmatize_all(items), all_native)
            self.assertEqual(
                cli_lem.lemmatize_all(["оквэд для цветочного магазина"], stopwords=GENERAL_STOP_WORDS),
                all_gen,
            )

        # dict_version, manifest, version_info, min_version, strict_version
        self.assertTrue(self.lem.dict_version)
        self.assertIsInstance(self.lem.manifest, dict)
        vi = self.lem.version_info()
        self.assertEqual(vi["package_version"], __version__)
        self.assertEqual(vi["library_version"], __version__)
        self.assertEqual(vi["dict_version"], self.lem.dict_version)
        with RuLemma(min_version=__version__, strict_version=True) as checked:
            self.assertEqual(checked.library_version, __version__)
        with self.assertRaises(RuntimeError):
            RuLemma(min_version="9.9.9")

    def test_v030a_technical_profile_and_bindings(self) -> None:
        # Homoglyph repair (pure-Python & native Go parity)
        for raw, want in [
            ("cм", "см"),
            ("рrо", "pro"),
            ("intеl", "intel"),
            ("аsus", "asus"),
            ("оzon", "ozon"),
            ("ryzеn", "ryzen"),
        ]:
            self.assertEqual(normalize_homoglyphs(raw), want)
            self.assertEqual(self.lem.normalize_homoglyphs(raw), want)

        # Critical technical non-collisions
        for a, b in [
            ("бензопила 2,6 л.с.", "бензопила 6,2 л.с."),
            ("плитка 100×100 см", "плитка 100 см"),
            ("коробка 500×500 мм", "коробка 500×500×500 мм"),
            ("блок питания 3,3 В", "блок питания 3 В"),
            ("зарядка 12 В", "зарядка 3 в 1"),
            ("зимние ботинки б/у", "зимние ботинки"),
            ("игры 18+", "игры 18"),
            ("книга c++", "книга c"),
            ("телефон 8/256", "телефон 8/128"),
        ]:
            self.assertNotEqual(
                self.lem.semantic_key(a, profile="technical"),
                self.lem.semantic_key(b, profile="technical"),
                f"technical collision: {a!r} vs {b!r}",
            )

        # Normalized technical equivalences
        for a, b in [
            ("термос 0,5 л", "термос 0.5 л"),
            ("винт 5 мм", "винт 5мм"),
            ("ssd m.2", "ssd м.2"),
            ("плитка 100×100 см", "плитка 100 x 100 cм"),
            ("велосипед б/у", "велосипед б / у"),
        ]:
            self.assertEqual(
                self.lem.semantic_key(a, profile="technical"),
                self.lem.semantic_key(b, profile="technical"),
                f"technical equivalence failed: {a!r} vs {b!r}",
            )

        # Preposition bindings, purpose bindings, keep_words, split_hyphens, compact_codes
        self.assertNotEqual(
            self.lem.key("кофе без сахара с молоком", preserve_prep_bindings=True),
            self.lem.key("кофе с сахаром без молока", preserve_prep_bindings=True),
        )
        self.assertNotEqual(
            self.lem.key("пластиковые окна", bind_purpose_prep=True),
            self.lem.key("пластик для окон", bind_purpose_prep=True),
        )
        self.assertNotEqual(
            self.lem.key("зеркальный пластик", keep_words={"пластик"}),
            self.lem.key("пластиковое зеркало", keep_words={"пластик"}),
        )
        self.assertEqual(
            self.lem.key("онлайн-курсы python", split_hyphens=True),
            self.lem.key("онлайн курсы python", split_hyphens=True),
        )
        self.assertEqual(
            self.lem.key("кроссовки nike p-6000", compact_codes=True),
            self.lem.key("кроссовки nike p 6000", compact_codes=True),
        )
        self.assertEqual(
            self.lem.key("кофемашина delonghi ecam 220", compact_codes=True),
            self.lem.key("кофемашина delonghi ecam220", compact_codes=True),
        )

        # tokenize_technical
        toks = self.lem.tokenize_technical("зимние ботинки б/у 100×100 см 12 В")
        texts = [t["text"] for t in toks]
        self.assertIn("б/у", texts)
        self.assertIn("100x100", texts)
        self.assertIn("12", texts)
        self.assertIn("в", texts)
        self.assertTrue(any(t["text"] == "в" and t["is_tech"] for t in toks))

        # Native batch vs CLI fallback parity for v0.3 technical profile & flags
        samples = [
            "бензопила 2,6 л.с.",
            "зимние ботинки б/у",
            "кофе без сахара с молоком",
            "пластик для окон",
            "винт 5мм",
        ]
        nat = self.lem.keys_batch(samples, preserve_prep_bindings=True, bind_purpose_prep=True)
        with RuLemma(workers=2, prefer_native=False) as cli_lem:
            cli = cli_lem.keys_batch(samples, preserve_prep_bindings=True, bind_purpose_prep=True)
            self.assertEqual(cli, nat)

    @unittest.skipUnless(hasattr(os, "fork"), "os.fork() unavailable on this platform")
    def test_v030a_fork_safety_contract(self) -> None:
        env = dict(os.environ)
        env["PYTHONPATH"] = str(ROOT / "python")

        # Case 1: RuLemma() loaded in parent -> fork -> child call raises RuntimeError and child exits cleanly (no deadlock)
        script_after_load = (
            "import os, sys\n"
            "from rulemma import RuLemma\n"
            "lem = RuLemma(workers=2)\n"
            "assert lem.word('кроссовки') == 'кроссовок'\n"
            "pid = os.fork()\n"
            "if pid == 0:\n"
            "    try:\n"
            "        lem.word('кроссовки')\n"
            "        os._exit(2)\n"
            "    except RuntimeError as e:\n"
            "        assert 'fork' in str(e).lower()\n"
            "        lem.close()\n"
            "        os._exit(0)\n"
            "_, status = os.waitpid(pid, 0)\n"
            "lem.close()\n"
            "sys.exit(os.waitstatus_to_exitcode(status))\n"
        )
        proc1 = subprocess.run([sys.executable, "-c", script_after_load], env=env, timeout=10, capture_output=True, text=True)
        self.assertEqual(proc1.returncode, 0, f"fork-after-load failed: {proc1.stderr}")

        # Case 2: import rulemma in parent, load RuLemma ONLY in child -> works cleanly
        script_before_load = (
            "import os, sys\n"
            "from rulemma import RuLemma\n"
            "pid = os.fork()\n"
            "if pid == 0:\n"
            "    with RuLemma(workers=2) as lem:\n"
            "        assert lem.word('кроссовки') == 'кроссовок'\n"
            "    os._exit(0)\n"
            "_, status = os.waitpid(pid, 0)\n"
            "sys.exit(os.waitstatus_to_exitcode(status))\n"
        )
        proc2 = subprocess.run([sys.executable, "-c", script_before_load], env=env, timeout=10, capture_output=True, text=True)
        self.assertEqual(proc2.returncode, 0, f"fork-before-load failed: {proc2.stderr}")

        # Case 3: multiprocessing 'spawn' works cleanly even after parent loaded RuLemma
        script_spawn = (
            "import multiprocessing as mp\n"
            "from rulemma import RuLemma\n"
            "def worker(q):\n"
            "    with RuLemma(workers=2) as lem:\n"
            "        q.put(lem.word('кроссовки'))\n"
            "if __name__ == '__main__':\n"
            "    with RuLemma(workers=2) as parent:\n"
            "        assert parent.word('кроссовки') == 'кроссовок'\n"
            "        ctx = mp.get_context('spawn')\n"
            "        q = ctx.Queue()\n"
            "        p = ctx.Process(target=worker, args=(q,))\n"
            "        p.start()\n"
            "        val = q.get(timeout=8)\n"
            "        p.join(timeout=8)\n"
            "        assert p.exitcode == 0 and val == 'кроссовок', (p.exitcode, val)\n"
        )
        with tempfile.TemporaryDirectory() as td:
            sp = Path(td) / "spawn_test.py"
            sp.write_text(script_spawn, encoding="utf-8")
            proc3 = subprocess.run([sys.executable, str(sp)], env=env, timeout=15, capture_output=True, text=True)
        self.assertEqual(proc3.returncode, 0, f"spawn failed: {proc3.stderr}")

    def test_v030b_morphology_and_prep_governs(self) -> None:
        pil = self.lem.analyze("пил")
        self.assertTrue(pil["known"])
        self.assertTrue(pil.get("is_homonym"))
        self.assertTrue(pil.get("has_noun"))
        cands = {(c["lemma"], c["pos"]) for c in self.lem.word_candidates("пил")}
        self.assertIn(("пила", "NOUN"), cands)
        self.assertIn(("пить", "VERB"), cands)

        self.assertTrue(self.lem.prep_governs("с", "цинком"))
        self.assertTrue(self.lem.prep_governs("к", "применению"))
        self.assertTrue(self.lem.prep_governs("в", "москве"))
        self.assertFalse(self.lem.prep_governs("с", "1000"))
        self.assertFalse(self.lem.prep_governs("к", "для"))
        self.assertFalse(self.lem.prep_governs("в", "д"))

        sc = self.lem.analyze("ножницы")
        self.assertTrue(sc.get("pltm"))
        self.assertEqual(sc["pos"], "NOUN")

    def test_v030c_inflection_and_agreement(self) -> None:
        self.assertEqual(self.lem.inflect("машина", "accs", "sing"), ("машину", True))
        self.assertEqual(self.lem.inflect("ножницы", "datv", "sing"), ("ножницам", True))
        self.assertEqual(self.lem.inflect("пальто", "loct", "sing"), ("пальто", True))
        self.assertEqual(self.lem.inflect("LG", "gent"), ("LG", False))

        self.assertEqual(
            self.lem.inflect_phrase("стиральная машина LG F2V5", "accs"),
            ("стиральную машину LG F2V5", True),
        )
        self.assertEqual(
            self.lem.inflect_phrase("Краснодарский край", "loct"),
            ("Краснодарском крае", True),
        )
        self.assertEqual(
            self.lem.inflect_phrase("зимние кроссовки Nike Air", "gent"),
            ("зимних кроссовок Nike Air", True),
        )
        self.assertEqual(
            self.lem.inflect_phrase("чехол для телефона", "gent"),
            ("чехла для телефона", True),
        )

        forms = self.lem.all_forms("сумка")
        self.assertIn("сумок", forms)
        self.assertIn("сумкой", forms)

        self.assertEqual(self.lem.agree_number(1, "товар"), "1 товар")
        self.assertEqual(self.lem.agree_number(2, "товар"), "2 товара")
        self.assertEqual(self.lem.agree_number(5, "товар"), "5 товаров")
        self.assertEqual(self.lem.agree_number(11, "товар"), "11 товаров")
        self.assertEqual(self.lem.agree_number(21, "товар"), "21 товар")
        self.assertEqual(self.lem.agree_number(1.5, "товар"), "1.5 товара")
        self.assertEqual(pluralize_form(21, "товар", "товара", "товаров"), "товар")

        for p, nw, want in [
            ("в", "вторник", "во"),
            ("с", "скидкой", "со"),
            ("к", "всем", "ко"),
            ("о", "акциях", "об"),
            ("о", "яблоках", "о"),
            ("о", "всем", "обо"),
        ]:
            self.assertEqual(preposition(p, nw), want)
            self.assertEqual(self.lem.preposition(p, nw), want)

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
