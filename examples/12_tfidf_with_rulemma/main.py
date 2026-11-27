"""12. Подготовка русскоязычного корпуса через rulemma.lemmatize_batch и расчёт TF-IDF.

Пример работает без внешних зависимостей на чистом Python stdlib; в комментариях показана
однострочная интеграция с scikit-learn TfidfVectorizer.
"""
from __future__ import annotations

import math
from collections import Counter

from rulemma import DEFAULT_STOP_WORDS, RuLemma


def compute_tfidf(tokenized_docs: list[list[str]]) -> list[dict[str, float]]:
    n_docs = len(tokenized_docs)
    df: Counter[str] = Counter()
    for doc in tokenized_docs:
        df.update(set(doc))

    vectors: list[dict[str, float]] = []
    for doc in tokenized_docs:
        tf = Counter(doc)
        total = max(1, len(doc))
        vec = {
            term: (cnt / total) * (math.log((1 + n_docs) / (1 + df[term])) + 1.0)
            for term, cnt in tf.items()
        }
        vectors.append(vec)
    return vectors


def main() -> None:
    corpus = [
        "Зимние кроссовки Найк с мембраной для бега по снегу",
        "Кофемашина Делонги с автоматическим капучинатором и кофемолкой",
        "Встраиваемые посудомоечные машины шириной 60 см для кухни",
        "Кроссовки для бега по асфальту и зимних тренировок",
    ]

    with RuLemma() as lem:
        # Быстрая пакетная лемматизация всего корпуса в горутинах Go
        lemmatized_docs = lem.lemmatize_batch(corpus)

        # Для scikit-learn достаточно передать lemmatized_docs напрямую:
        #   from sklearn.feature_extraction.text import TfidfVectorizer
        #   X = TfidfVectorizer(stop_words=list(DEFAULT_STOP_WORDS)).fit_transform(lemmatized_docs)
        clean_tokens = [
            [tok for tok in doc.split() if tok not in DEFAULT_STOP_WORDS and len(tok) > 1]
            for doc in lemmatized_docs
        ]
        vectors = compute_tfidf(clean_tokens)

        assert "кроссовок" in vectors[0] and "кроссовок" in vectors[3]
        assert "см" in vectors[2] and "кофемашина" in vectors[1]

        for i, vec in enumerate(vectors):
            top3 = sorted(vec.items(), key=lambda kv: kv[1], reverse=True)[:3]
            print(f"Док #{i + 1}: " + ", ".join(f"{t}={w:.3f}" for t, w in top3))


if __name__ == "__main__":
    main()
