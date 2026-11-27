"""08. Частотный словарь лемм, водность, академическая тошнота и биграммы текста."""
from __future__ import annotations

import math
from collections import Counter

from rulemma import DEFAULT_STOP_WORDS, RuLemma


def main() -> None:
    text = (
        "Зимние кроссовки для бега защищают ноги от холода. "
        "Хорошие кроссовки на зиму имеют прочную подошву и тёплую подкладку. "
        "Выбирая зимние кроссовки, обращайте внимание на сцепление подошвы со снегом."
    )

    with RuLemma() as lem:
        all_lemmas = lem.lemmatize_tokens(text)
        content_lemmas = [w for w in all_lemmas if w not in DEFAULT_STOP_WORDS and len(w) > 1]

        freq = Counter(content_lemmas)
        bigrams = Counter(zip(content_lemmas, content_lemmas[1:]))

        water_pct = 100.0 * (len(all_lemmas) - len(content_lemmas)) / len(all_lemmas)
        top_word, top_count = freq.most_common(1)[0]
        classic_nausea = math.sqrt(top_count)

        assert top_word == "кроссовок" and top_count == 3, (top_word, top_count)
        assert ("зимний", "кроссовок") in bigrams

        print(f"Всего токенов: {len(all_lemmas)}, значимых лемм: {len(content_lemmas)}, водность: {water_pct:.1f}%")
        print(f"Классическая тошнота: {classic_nausea:.2f} (лидер: {top_word!r}, {top_count}x)")
        print("ТОП-5 лемм:   ", freq.most_common(5))
        print("ТОП-3 биграмм:", bigrams.most_common(3))


if __name__ == "__main__":
    main()
