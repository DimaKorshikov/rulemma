"""Unit tests for the rulemma Python package (both native CGO shared lib & CLI fallback)."""
from __future__ import annotations

import time
import unittest
from pathlib import Path

from rulemma import RuLemma


class TestRuLemmaPython(unittest.TestCase):
    @classmethod
    def setUpClass(cls) -> None:
        cls.lem = RuLemma(workers=4)

    @classmethod
    def tearDownClass(cls) -> None:
        cls.lem.close()

    def test_backend_is_native(self) -> None:
        self.assertEqual(self.lem.backend, "native")
        st = self.lem.stats()
        self.assertGreaterEqual(st["lemmas"], 150_000)
        self.assertGreaterEqual(st["adj_noun"], 3_500)

    def test_single_word_and_ecommerce_fixes(self) -> None:
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
        }
        for w, want in cases.items():
            self.assertEqual(self.lem.word(w), want, f"word({w!r})")

    def test_lemma_key_and_semantic_key(self) -> None:
        self.assertEqual(
            self.lem.lemma_key("Купить зимние кроссовки Найк"),
            self.lem.lemma_key("кроссовки найки зимний"),
        )
        # Relational adj -> noun canonicalization!
        self.assertEqual(
            self.lem.semantic_key("зимние кроссовки найк"),
            self.lem.semantic_key("кроссовки на зиму найк"),
        )
        self.assertEqual(
            self.lem.semantic_key("детский рюкзак"),
            self.lem.semantic_key("рюкзак для детей"),
        )
        self.assertEqual(
            self.lem.semantic_key("детский рюкзак"),
            self.lem.semantic_key("рюкзак для ребенка"),
        )
        self.assertEqual(
            self.lem.semantic_key("школьная форма"),
            self.lem.semantic_key("форма в школу"),
        )
        # Negative collision guards
        self.assertNotEqual(
            self.lem.semantic_key("умные часы"),
            self.lem.semantic_key("часы ум"),
        )
        self.assertNotEqual(
            self.lem.semantic_key("мороженая рыба"),
            self.lem.semantic_key("мороженое рыба"),
        )
        self.assertNotEqual(
            self.lem.semantic_key("ручной отпариватель"),
            self.lem.semantic_key("отпариватель для рук"),
        )

    def test_adj_to_noun_and_seo_name(self) -> None:
        noun, ok = self.lem.adj_to_noun("зимние")
        self.assertTrue(ok)
        self.assertEqual(noun, "зима")

        agreed, changed = self.lem.agree_adjective("синие", "Сумка")
        self.assertTrue(changed)
        self.assertEqual(agreed, "синяя")

        seo, added, n_agreed = self.lem.build_seo_name("Сумка кожаная", "Сумка", ["кожаный", "синие", "женские"], 100)
        self.assertEqual(added, 2)
        self.assertGreaterEqual(n_agreed, 2)
        self.assertIn("синяя", seo)
        self.assertIn("женская", seo)

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
