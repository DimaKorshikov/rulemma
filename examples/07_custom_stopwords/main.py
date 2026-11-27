"""07. Кастомные стоп-слова в lemma_key, semantic_key и батчевых вызовах Python."""
from __future__ import annotations

from rulemma import DEFAULT_STOP_WORDS, RuLemma


def main() -> None:
    query = "Купить зимние кроссовки Найк в Москве недорого"

    with RuLemma() as lem:
        # 1. Дефолтные стоп-слова (предлоги + базовый коммерческий хвост)
        default_key = lem.lemma_key(query)
        assert default_key == "зимний кроссовок москва найк", default_key

        # 2. Расширяем стандартный список гео-словами
        geo_stop = DEFAULT_STOP_WORDS | {"москва", "спб", "россия"}
        no_geo_key = lem.lemma_key(query, stopwords=geo_stop)
        assert no_geo_key == "зимний кроссовок найк", no_geo_key

        # 3. Сохраняем коммерческий интент «купить», фильтруя только предлоги и города
        keep_intent_key = lem.lemma_key(query, stopwords={"в", "москва", "недорого"})
        assert keep_intent_key == "зимний кроссовок купить найк", keep_intent_key

        # 4. Отключаем стоп-слова полностью (stopwords=[])
        raw_key = lem.lemma_key("Купить в Москве", stopwords=[])
        assert raw_key == "в купить москва", raw_key

        # 5. Батчевый semantic_keys с кастомным стоп-листом в горутинах Go
        batch = ["Купить зимние кроссовки в Москве", "Кроссовки на зиму в СПб"]
        sem_keys = lem.semantic_keys(batch, stopwords=geo_stop)
        assert sem_keys[0] == sem_keys[1] == "зима кроссовок", sem_keys

        print("По умолчанию:   ", default_key)
        print("Без гео:        ", no_geo_key)
        print("С «купить»:     ", keep_intent_key)
        print("Без стоп-слов:  ", raw_key)
        print("Батч без гео:   ", sem_keys)


if __name__ == "__main__":
    main()
