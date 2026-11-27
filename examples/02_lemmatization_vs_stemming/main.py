"""02. Лемматизация (rulemma) против стемминга: почему основы ломают поиск и аналитику."""
from __future__ import annotations

from rulemma import RuLemma

# Упрощённые демонстрационные основы (как их обрезает типичный алгоритмический стеммер)
STEMMER_DEMO = {
    "людей": "люд",
    "человек": "человек",
    "детей": "дет",
    "ребенка": "ребенк",
    "лучшие": "лучш",
    "хорошая": "хорош",
    "огурцов": "огурц",
    "огурец": "огурец",
    "кружки": "кружк",
    "кружек": "кружек",
}


def main() -> None:
    pairs = [
        ("людей", "человек"),
        ("детей", "ребенка"),
        ("лучшие", "хорошая"),
        ("огурцов", "огурец"),
        ("кружки", "кружек"),
    ]
    with RuLemma() as lem:
        print(f"{'Пара форм':24s} | {'Стеммер (основы)':22s} | {'rulemma (леммы)':22s}")
        print("-" * 74)
        for a, b in pairs:
            sa, sb = STEMMER_DEMO[a], STEMMER_DEMO[b]
            la, lb = lem.word(a), lem.word(b)
            assert sa != sb, f"stemmer demo should differ for {a}/{b}"
            assert la == lb, f"rulemma should match {a}/{b}: {la} != {lb}"
            print(f"{a + ' / ' + b:24s} | {sa + ' != ' + sb:22s} | {la + ' == ' + lb:22s}")


if __name__ == "__main__":
    main()
