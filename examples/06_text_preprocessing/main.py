"""06. Чистый препроцессинг русского текста: пунктуация, ё -> е, токены и леммы."""
from __future__ import annotations

from rulemma import RuLemma, normalize_phrase


def main() -> None:
    raw_texts = [
        "  Чёрная  кофемашина De'Longhi (1450 Вт) — цена 49 990 руб.!  ",
        "Ёлка-иголка   искусственная,   180 см,  со  снегом...",
        "Витамин   Е   +   витамин   Д3   (капсулы,   60 шт.)",
    ]

    with RuLemma() as lem:
        for text in raw_texts:
            norm = normalize_phrase(text)
            norm_yo = normalize_phrase(text, keep_yo=True)
            tokens = lem.lemmatize_tokens(text)
            assert norm == lem.normalize_phrase(text)
            assert norm_yo == lem.normalize_phrase(text, keep_yo=True)
            print("Исходник:    ", text.strip())
            print("Норм (ё->е): ", norm)
            print("Норм (с ё):  ", norm_yo)
            print("Леммы:       ", tokens)
            print()


if __name__ == "__main__":
    main()
