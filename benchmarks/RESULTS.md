# Результаты бенчмарка rulemma против библиотек русского NLP

Замер выполнен на Linux x86_64 (CPU потоков: 32), Python 3.12.3, Go 1.23.
Наборы данных: общий русский (133 слова), e-commerce и поиск (120 сложных терминов), пары дедупликации (55 пар), батч (20 000 уникальных запросов).

| Библиотека | Архитектура | Общий RU | E-Commerce и поиск | Дедупликация F1 (включая adj->noun) | Холодный старт | 1 запрос | Батч из Python |
|---|---|---:|---:|---:|---:|---:|---:|
| **[rulemma](../README.md) (Go CGO native)** | Dict + Rules (Go, C-shared) | 96.2% | 99.2% | 100.0% (FP=0) | 209 мс | 134.8 мкс | **602,753 запр/с** |
| **[pymorphy3](https://github.com/no-plagiarism/pymorphy3) (без кэша)** | OpenCorpora FST (Python) | 94.7% | 41.7% | 18.2% (FP=0) | 84 мс | 562.2 мкс | **1,778 запр/с** |
| **[pymorphy3](https://github.com/no-plagiarism/pymorphy3) (`lru_cache` 200k)** | OpenCorpora FST + Cache (Py) | 94.7% | 41.7% | 18.2% (FP=0) | 84 мс | 6.5 мкс | **152,871 запр/с** |
| **[Natasha](https://github.com/natasha/natasha) ([Slovnet](https://github.com/natasha/slovnet) + MorphVocab)** | Neural POS + OpenCorpora (Py) | 95.5% | 74.2% | 13.6% (FP=1) | 723 мс | 920.8 мкс | **1,086 запр/с** |
| **[simplemma](https://github.com/adbar/simplemma) (`ru`)** | Dict + Rules (Python) | 93.2% | 90.0% | 14.0% (FP=0) | 1463 мс | 5.1 мкс | **195,221 запр/с** |
| **[SnowballStemmer](https://github.com/snowballstem/snowball) (`ru`)** | Rule Stemmer (Python) | 16.5% | 49.2% | 29.2% (FP=1) | 0 мс | 156.6 мкс | **6,386 запр/с** |
| **[pymystem3](https://github.com/nlpub/pymystem3) ([Яндекс MyStem](https://yandex.ru/dev/mystem/))** | C++ Binary Bridge (Proprietary) | 92.5% | 89.2% | 22.2% (FP=0) | 778 мс | 195.8 мкс | **5,634 запр/с** |

## Примечания к методике

- Все три тестовых набора (`gold_general_ru.tsv`, `gold_ecommerce_ru.tsv`, `gold_dedup_pairs.tsv`) составлены авторами `rulemma` и открыты в папке `benchmarks/`.
- В наборе дедупликации 35 из 40 положительных пар проверяют склейку относительных прилагательных с существительными (`зимние кроссовки` и `кроссовки на зиму`). У сторонних библиотек такого словаря нет по дизайну; без `adj->noun` метод `rulemma.lemma_key` набирает те же 22.2%, а `rulemma.semantic_key` набирает 100.0% (FP=0).
- `SnowballStemmer` является стеммером и возвращает основы слов, а не словарные леммы.
- Воспроизвести замер: `.venv-bench/bin/python benchmarks/compare_models.py`.
