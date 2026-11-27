"""09. Дедупликация поисковых запросов (lemma_key / semantic_key) и матчинг тегов."""
from __future__ import annotations

from collections import defaultdict

from rulemma import RuLemma


def main() -> None:
    queries = [
        ("купить зимние кроссовки найк", 12_400),
        ("кроссовки на зиму найк цена", 5_100),
        ("найки зимние кроссовки недорого", 2_300),
        ("детский рюкзак для школы", 8_900),
        ("школьный рюкзак для детей", 6_400),
        ("рюкзак в школу для ребенка", 3_100),
        ("умные часы мужские", 19_000),
        ("часы ум мужские", 50),
    ]

    with RuLemma() as lem:
        sem_keys = lem.semantic_keys([q for q, _ in queries])

        clusters: dict[str, list[tuple[str, int]]] = defaultdict(list)
        for (q, vol), sk in zip(queries, sem_keys):
            clusters[sk].append((q, vol))

        # Все 3 варианта зимних кроссовок Найк и 3 варианта школьного рюкзака склеились,
        # а ловушка «умные часы» vs «часы ум» осталась в разных кластерах!
        assert len(clusters["зима кроссовок найк"]) == 3
        assert len(clusters["ребенок рюкзак школа"]) == 3
        assert lem.semantic_key("умные часы мужские") != lem.semantic_key("часы ум мужские")

        print(f"Исходных запросов: {len(queries)} -> уникальных кластеров: {len(clusters)}\n")
        for key, items in clusters.items():
            total_vol = sum(v for _, v in items)
            print(f"[{key}] (суммарный спрос: {total_vol:,})")
            for q, v in items:
                print(f"    - {q} ({v:,})")


if __name__ == "__main__":
    main()
