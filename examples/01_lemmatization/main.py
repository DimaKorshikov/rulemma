"""01. Базовая лемматизация слов и фраз на Python, дефисные слова и ё -> е."""
from __future__ import annotations

from rulemma import RuLemma, normalize_phrase


def main() -> None:
    with RuLemma() as lem:
        # Одиночные слова (включая дефисные составные и ё -> е)
        samples = {
            "кроссовки": "кроссовок",
            "чёрные": "черный",
            "роботов-пылесосов": "робот-пылесос",
            "отпариватели": "отпариватель",
            "дочерей": "дочь",
            "см": "см",
        }
        for word, expected in samples.items():
            got = lem.word(word)
            assert got == expected, f"{word}: {got!r} != {expected!r}"
            print(f"  {word:20s} -> {got}")

        # Лемматизация целой фразы с сохранением порядка токенов
        phrase = "Купить чёрные роботы-пылесосы для уборки, 35 см!"
        lemmas = lem.lemmatize(phrase)
        assert lemmas == "купить черный робот-пылесос для уборка 35 см", lemmas
        print("\nФраза:      ", phrase)
        print("Нормализация:", normalize_phrase(phrase))
        print("С буквой ё: ", normalize_phrase(phrase, keep_yo=True))
        print("Леммы:      ", lemmas)


if __name__ == "__main__":
    main()
