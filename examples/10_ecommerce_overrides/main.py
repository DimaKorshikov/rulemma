"""10. Подключение доменного файла overrides.tsv без пересборки базовых словарей."""
from __future__ import annotations

from pathlib import Path

from rulemma import RuLemma


def main() -> None:
    custom_ov = Path(__file__).resolve().parent / "custom_overrides.tsv"

    with RuLemma(overrides=custom_ov) as lem:
        checks = [
            ("квадрокоптеры", "дрон"),
            ("ушм", "болгарка"),
            ("макбуки", "макбук"),
            ("эйрподсы", "эйрподс"),
        ]
        for word, want in checks:
            got = lem.word(word)
            assert got == want, f"{word}: got {got!r}, want {want!r}"
            print(f"  {word:18s} -> {got}")

        k1 = lem.lemma_key("Купить УШМ Макита 125 мм")
        k2 = lem.lemma_key("Болгарка Макита 125 мм")
        assert k1 == k2 == "125 болгарка макита мм", (k1, k2)
        print(f"\nЕдиный ключ для УШМ и болгарки: {k1!r}")


if __name__ == "__main__":
    main()
