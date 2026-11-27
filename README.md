# rulemma

[![CI](https://github.com/DimaKorshikov/rulemma/actions/workflows/ci.yml/badge.svg)](https://github.com/DimaKorshikov/rulemma/actions/workflows/ci.yml)
[![Website](https://img.shields.io/badge/website-rulemma.ru-1f6feb)](https://rulemma.ru/)
[![Go Version](https://img.shields.io/badge/Go-1.21%2B-00ADD8?logo=go)](https://go.dev/)
[![Python](https://img.shields.io/badge/Python-3.9%2B-3776AB?logo=python)](https://www.python.org/)
[![License: MIT](https://img.shields.io/badge/License-MIT-green.svg)](LICENSE)

**Сайт:** [rulemma.ru](https://rulemma.ru/) · **Исходный код:** [github.com/DimaKorshikov/rulemma](https://github.com/DimaKorshikov/rulemma) · **История изменений:** [CHANGELOG.md](CHANGELOG.md)

Словарный лемматизатор, компактный морфоанализатор и движок склонения русского языка и поисковых e-commerce запросов на Go с нативными Python-биндингами (`ctypes` к `librulemma.so` / `.dylib`) без внешних зависимостей. Умеет лемматизировать слова и тексты, нормализовать фразы (`ё -> е`) и смешанные RU/EN омоглифы, строить канонические и технические ключи (`2,6 л.с.`, `100×100 см`, `б/у`, `m.2`, `18+`, `12 В`, связки предлогов), сводить относительные прилагательные к существительным, разбирать грамматические признаки и омонимы (`analyze`, `prep_governs`), склонять слова и короткие именные группы (`inflect_phrase`), согласовывать числа (`21 товар`) и собирать SEO-названия.

Авторы: **Коршиков Дмитрий и Gemini**. Лицензия: **MIT**.

## Зачем появилась эта библиотека

Стандартные морфологические анализаторы русского языка создавались под художественную прозу и новости. Когда на вход поступают миллионы коротких поисковых запросов и карточек товаров, возникают три типичные проблемы:

1. Галлюцинации на новых словах, брендах в кириллице, единицах измерения и омонимах. Анализатор без контекста ошибочно преобразует `спрей` в `спреть`, `кофемашина` в `кофемашин`, `худи` в `худить`, `сяоми` в `сяомь`, `см` в `смотреть`, `витамин е` в `витамин быть`, а причастия вроде `оцинкованные` и `встраиваемые` — в глаголы `оцинковать`/`встраивать`.
2. Разъезжаются одинаковые по смыслу запросы (и склеиваются разные по характеристикам). Обычный лемматизатор оставляет `зимние кроссовки` и `кроссовки на зиму`, `детский рюкзак` и `рюкзак для детей` в разных кластерах, а наивная сортировка токенов склеивает `2,6 л.с.` с `6,2 л.с.`, `100×100 см` со `100 см` и `без сахара с молоком` с `с сахаром без молока`.
3. Производительность на больших массивах. В Python разбор десятков миллионов запросов через нейросетевые теггеры или внешние процессы упирается в тысячи запросов в секунду, GIL и сотни мегабайт RAM.

`rulemma` решает эти задачи за счёт очищенного словаря (`214 196` словоформ, словарно-проверяемый OOV-fallback без обрубков), таблицы из `3 960` проверенных пар `прилагательное -> существительное`, `74 098` форм согласования, приоритетного `data/overrides.tsv` (`470` записей), лениво подгружаемых компактных таблиц морфологии (`127 163` словоформ + `11 909` омонимов + парадигмальный OOV-вывод) и склонения (`44 819` записей: `347` парадигм на `44 472` лемм), а также пакетной обработки в горутинах Go.

## Быстрый старт на Python

Для сборки нативной библиотеки `librulemma.so` (или `.dylib` на macOS) нужны **Go 1.21+** и компилятор C (`gcc` или `clang`). В рантайме Python не требуется ни одного стороннего пакета.

```bash
git clone https://github.com/DimaKorshikov/rulemma.git
cd rulemma
make build            # собирает bin/rulemma и python/rulemma/librulemma.so
pip install .
```

```python
from rulemma import (
    DEFAULT_STOP_WORDS,
    GENERAL_STOP_WORDS,
    RuLemma,
    normalize_homoglyphs,
    normalize_phrase,
    preposition,
)

# Быстрая нормализация фразы и ремонт смешанных кириллица+латиница омоглифов
print(normalize_phrase("  Чёрная кофемашина, 35 см! "))               # "черная кофемашина 35 см"
print(normalize_homoglyphs("Кабель 5cм intеl рrо"))                   # "Кабель 5см intel pro"

with RuLemma(min_version="0.4.0") as lem:
    print(lem.backend, lem.library_version, lem.abi_version)  # native 0.4.0 1
    print(lem.dict_version)                                   # 2026.11.5

    # Лемматизация слов и фраз (буква ё нормализуется в е) + источник решения
    print(lem.word("спрей"))                        # "спрей"
    print(lem.word("см"))                           # "см" (не "смотреть"!)
    print(lem.word("роботов-пылесосов"))            # "робот-пылесос"
    print(lem.lookup_word("судебного"))             # ("судебный", "rule")
    print(lem.lemmatize("Купить зимние кроссовки")) # "купить зимний кроссовок"

    # Канонический ключ: дефолт обратно совместим с v0.2, а profile="technical" бережёт характеристики
    assert lem.semantic_key("шоколад без сахара") != lem.semantic_key("шоколад с сахаром")
    assert lem.semantic_key("зимние кроссовки найк") == lem.semantic_key("кроссовки на зиму найк")
    assert lem.semantic_key("бензопила 2,6 л.с.", profile="technical") != lem.semantic_key("бензопила 6,2 л.с.", profile="technical")
    assert lem.semantic_key("термос 0,5 л", profile="technical") == lem.semantic_key("термос 0.5 л", profile="technical")
    assert lem.semantic_key("винт 5 мм", profile="technical") == lem.semantic_key("винт 5мм", profile="technical")
    print(lem.semantic_key("зимние ботинки б/у", profile="technical"))  # "б/у ботинок зима"

    # Связки предлогов, защита главного слова, дефисы и компактные коды моделей
    assert lem.key("кофе без сахара с молоком", preserve_prep_bindings=True) != lem.key("кофе с сахаром без молока", preserve_prep_bindings=True)
    assert lem.key("пластиковые окна", bind_purpose_prep=True) != lem.key("пластик для окон", bind_purpose_prep=True)
    assert lem.key("зеркальный пластик", keep_words={"пластик"}) != lem.key("пластиковое зеркало", keep_words={"пластик"})

    # Компактный морфоанализ и управление предлогов (подгружаются лениво при первом вызове)
    print(lem.analyze("пил")["candidates"])         # пить (VERB) | пил (NOUN) | пила (NOUN)
    assert lem.prep_governs("с", "цинком") is True
    assert lem.prep_governs("с", "1000") is False   # "витамин С 1000" — не предлог

    # Склонение слов и коротких именных групп (защита брендов, артикулов, Pltm, Fixd и хвостов)
    print(lem.inflect_phrase("стиральная машина LG F2V5", "accs"))  # ("стиральную машину LG F2V5", True)
    print(lem.inflect_phrase("Краснодарский край", "loct"))         # ("Краснодарском крае", True)
    print(lem.agree_number(21, "товар"), lem.agree_number(1.5, "товар"))  # "21 товар", "1.5 товара"
    print(preposition("с", "скидкой"), preposition("о", "акциях"))        # "со", "об"

    # Однопроходный параллельный батч в горутинах Go (без GIL): сразу (lem, lemma_key, semantic_key)
    queries = ["зимние кроссовки найк", "кроссовки на зиму найк", "кофемашина делонги"]
    print(lem.lemmatize_all(queries, profile="technical"))
    print(lem.semantic_keys(queries, profile="technical"))

    # Согласование прилагательных/причастий + сборка SEO-заголовка
    print(lem.agree_adjective("оцинкованные", "Ведро"))  # ("оцинкованное", True)
    print(lem.build_seo_name("Сумка кожаная", "Сумка", ["кожаный", "синие", "женские"], 100))
    # ("Сумка кожаная, синяя, женская", 2, 2)
```

Если вы обрабатываете тысячи и миллионы строк из Python, передавайте их списками в `lemmatize_all`, `lemmatize_batch`, `lemma_keys`, `semantic_keys`, `keys_batch`, `lemmatize_and_keys` или `normalize_batch`. Переход между `ctypes` и `CGO` выполняется один раз на весь батч, а обработка идёт параллельно на всех ядрах CPU.

> **Многопроцессность и `os.fork()`:** рантайм Go запускает фоновые потоки при первой загрузке `librulemma.so`. В Python используйте `multiprocessing.get_context("spawn")` либо создавайте `RuLemma()` только **после** `fork()`. При вызове методов в форкнутом потомке после загрузки библиотеки в родителе `rulemma` безопасно поднимает `RuntimeError` вместо зависания (а `close()`/`__del__` не входят в Go).

## Быстрый старт на Go и CLI

Подключение как обычного Go-модуля (внешних зависимостей нет, потокобезопасность проверена через `go test -race ./...`):

```bash
go get github.com/DimaKorshikov/rulemma
```

В Go доступны три способа инициализации: загрузка словарей с диска (`rulemma.Load("data")`), подключение одной строкой через опциональный субпакет `rulemma/embedded` (`//go:embed`, встраивает `2.6 МБ` всех 8 таблиц, или `1.2 МБ` ядра при сборке с `-tags rulemma_core`), либо загрузка напрямую из памяти (`rulemma.LoadMemory` / `rulemma.NewMemoryFS` + отложенная `lem.LoadTable(name, bytes)` для WebAssembly и облачных сервисов):

```go
package main

import (
    "fmt"
    "log"

    "github.com/DimaKorshikov/rulemma"
    "github.com/DimaKorshikov/rulemma/embedded"
)

func main() {
    lem, err := embedded.Load() // или rulemma.Load("data")
    if err != nil {
        log.Fatal(err)
    }

    fmt.Println(rulemma.NormalizePhrase("Чёрная, 35 см!", false)) // черная 35 см
    fmt.Println(lem.Word("отпариватели"))                         // отпариватель
    fmt.Println(lem.SemanticKey("зимние кроссовки найк"))         // зима кроссовок найк

    // Опциональный технический профиль v0.3
    tech := rulemma.KeyOptions{Semantic: true, Profile: "technical", PreservePrepBindings: true}
    fmt.Println(lem.KeyWithOptions("кофе без сахара с молоком", tech)) // без_сахар кофе молоко

    // Компактная морфология и склонение (таблицы подгружаются лениво и потокобезопасно)
    fmt.Println(lem.PrepGoverns("с", "цинком"))                        // true
    inf, _ := lem.InflectPhrase("стиральная машина LG F2V5", "accs", "")
    fmt.Println(inf, "|", lem.AgreeNumber(21, "товар"))                // стиральную машину LG F2V5 | 21 товар
}
```

Для потоковой обработки крупных файлов есть бинарник `bin/rulemma`:

```bash
make build-cli

# Версия движка/ABI и статистика загруженных словарей + manifest.json
./bin/rulemma version
./bin/rulemma info --data data

# Поток через stdin (режимы: text | key | semkey | both | all | norm; профили: default | technical)
printf "зимние ботинки б/у\nкофе без сахара с молоком\n" | ./bin/rulemma lemmatize --mode semkey --profile technical --bind-preps

# Морфологический разбор и склонение из CLI
./bin/rulemma analyze пил
./bin/rulemma inflect --case accs стиральная машина LG F2V5

# Параллельная обработка TSV-файла по заданной колонке
./bin/rulemma tsv --in queries.tsv --out queries_lem.tsv --col 0 --mode all --header --workers 16
```

## Сравнение с конкурентами и бенчмарк

Мы сравнили `rulemma 0.4.0` с семью популярными библиотеками для русского языка одним воспроизводимым скриптом [`benchmarks/compare_models.py`](benchmarks/compare_models.py). Каждая библиотека запускалась в **отдельном изолированном процессе** для честного замера пикового RSS RAM (набор батча: `20 000` уникальных 7-словных запросов, `751` уникальных токенов):

- [pymorphy3](https://github.com/no-plagiarism/pymorphy3) `2.0.6` (MIT): морфологический анализатор на словаре OpenCorpora (без кэша и с `functools.lru_cache(maxsize=200_000)`).
- [mawo-pymorphy3](https://pypi.org/project/mawo-pymorphy3/) `1.0.4` (MIT): оптимизированный форк `pymorphy3`.
- [SteosMorphy](https://github.com/SteosOfficial/SteosMorphy-py) `1.1.1` (MIT): морфологический анализатор на Go DAWG с Python-обёрткой через `ctypes` и JSON.
- [Natasha](https://github.com/natasha/natasha) `1.6.0` + [Slovnet](https://github.com/natasha/slovnet) `0.6.0` (MIT): полный контекстный нейросетевой пайплайн (`Segmenter`, `NewsEmbedding`, `NewsMorphTagger`, `MorphVocab`).
- [simplemma](https://github.com/adbar/simplemma) `2.0.0` (MIT): многоязычный словарно-правильный лемматизатор на Python (`lang="ru"`).
- [pymystem3](https://github.com/nlpub/pymystem3) `0.2.0` (обёртка MIT, бинарник [Яндекс MyStem](https://yandex.ru/dev/mystem/) — проприетарный).
- [SnowballStemmer](https://github.com/snowballstem/snowball) `3.1.1` (BSD-3-Clause): алгоритмический стеммер (нижняя базовая линия).

Замер выполнен на Linux `x86_64`, процессор AMD Ryzen 9 5950X (16 ядер / 32 потока), Python `3.12.3`, Go `1.23`. Полный сырой вывод лежит в [`benchmarks/results.json`](benchmarks/results.json) и [`benchmarks/RESULTS.md`](benchmarks/RESULTS.md).

| Библиотека | Архитектура и лицензия | Общий RU (133) | E-Commerce (120) | Дедупликация F1 (55) | Холодный старт | Пик RSS RAM | 1 запрос | Батч из Python (20 000) |
|---|---|---:|---:|---:|---:|---:|---:|---:|
| **rulemma 0.4.0** (Go CGO) | Байтовая арена + правила (Go, MIT) | **99.2%** | **100.0%** | **100.0% (FP=0)** | 93 мс | 65.3 МБ* | 132.3 мкс | **552 213 запр/с** |
| [pymorphy3](https://github.com/no-plagiarism/pymorphy3) (без кэша) | OpenCorpora FST (Python, MIT) | 94.7% | 41.7% | 18.2% (FP=0) | 43 мс | 45.7 МБ | 394.8 мкс | 2 532 запр/с |
| [pymorphy3](https://github.com/no-plagiarism/pymorphy3) (`lru_cache` 200k) | OpenCorpora FST + кэш (Python, MIT) | 94.7% | 41.7% | 18.2% (FP=0) | 43 мс | 45.7 МБ | **5.6 мкс** | 178 404 запр/с |
| [mawo-pymorphy3](https://pypi.org/project/mawo-pymorphy3/) 1.0.4 | Форк pymorphy3 (Python, MIT) | 81.2% | 76.7% | 14.0% (FP=0) | 18 мс | 39.8 МБ | 452.0 мкс | 2 231 запр/с |
| [SteosMorphy](https://github.com/SteosOfficial/SteosMorphy-py) 1.1.1 | Go DAWG + JSON ctypes (MIT) | 84.2% | 76.7% | 18.2% (FP=0) | 330 мс | 544.9 МБ | 3 165.9 мкс | 779 запр/с |
| [Natasha](https://github.com/natasha/natasha) ([Slovnet](https://github.com/natasha/slovnet)) | Нейросеть POS + OpenCorpora (MIT) | 95.5% | 74.2% | 13.6% (FP=1) | 408 мс | 234.4 МБ | 818.5 мкс | 1 221 запр/с |
| [simplemma](https://github.com/adbar/simplemma) (`ru`) | Словарь + правила (Python, MIT) | 93.2% | 90.0% | 14.0% (FP=0) | 1 033 мс | 163.4 МБ | 8.0 мкс | 125 178 запр/с |
| [pymystem3](https://github.com/nlpub/pymystem3) ([MyStem](https://yandex.ru/dev/mystem/)) | C++ бинарник (проприетарный) | 92.5% | 89.2% | 22.2% (FP=0) | 493 мс | 32.8 МБ† | 137.2 мкс | 7 242 запр/с |
| [SnowballStemmer](https://github.com/snowballstem/snowball) (`ru`) | Стеммер (Python, BSD-3) | 16.5%‡ | 49.2%‡ | 29.2% (FP=1) | **0 мс** | 21.8 МБ | 126.3 мкс | 7 916 запр/с |

*\*У `rulemma 0.4.0` за счёт байтовой арены без GC-указателей сразу после загрузки базовых словарей RSS процесса составляет **`37.1 МБ`** (включая интерпретатор Python и 20k строк теста; в чистом процессе Python `30.8 МБ` — прирост `+16.0 МБ`, в чистом Go `15.6 МБ` RSS / `11.3 МБ` HeapAlloc; меньше `pymorphy3` с его `45.0 МБ`); пик `65.3 МБ` возникает на параллельном батче из 20 000 строк. У `SteosMorphy` распакованный словарь `morph.dawg` занимает `445 МБ` на диске против `2.6 МБ` у `rulemma`.*  
*†В `pymystem3` бинарник `mystem` работает в отдельном дочернем процессе, поэтому `RUSAGE_SELF` показывает только Python-обёртку.*  
*‡`SnowballStemmer` возвращает усечённые основы, а не словарные леммы.*

На масштабировании из Python (уникальные 7-словные запросы) `rulemma 0.4.0` обрабатывает **1 000 000 запросов за `1.47–1.60 с`** (`623 888 запр/с` для `lemmatize_batch` и **`682 309 запр/с`** для `semantic_keys`; однопроходный `lemmatize_all` даёт все 3 колонки на `~1.2–1.4×` быстрее 3 раздельных вызовов). В чистом Go (`go test -bench=BenchmarkLemmaKey -benchmem .`) один вызов `LemmaKey` требует ~`3.6 мкс`, `664 Б` памяти и `20` аллокаций; загрузка базовых словарей `Load("data")` занимает ~`59 мс` (`11.3 МБ` `HeapAlloc`, `5 522` heap-объектов).

## Открытые оговорки по методике замера

1. **Авторство тестовых наборов.** Все три файла ([`benchmarks/gold_general_ru.tsv`](benchmarks/gold_general_ru.tsv), [`benchmarks/gold_ecommerce_ru.tsv`](benchmarks/gold_ecommerce_ru.tsv), [`benchmarks/gold_dedup_pairs.tsv`](benchmarks/gold_dedup_pairs.tsv)) подготовлены авторами `rulemma`. В `v0.2.1` мы закрыли 5 реальных словарных промахов, обнаруженных при выпуске `v0.2.0` (`отпариватели`, `дочерей`, `воробьев`, `автодорог`, `большие`), и зафиксировали все изменения в [CHANGELOG.md](CHANGELOG.md) и золотых тестах [`tests/golden/`](tests/golden/). Единственным оставшимся расхождением на `gold_general_ru.tsv` (`132/133`, `99.2%`) остаётся честный бесконтекстный омоним `цветов -> цвет` (в тесте ожидается `цветок`).
2. **Почему в колонке дедупликации F1 у конкурентов 13.6% до 22.2%.** Из 40 положительных пар в `gold_dedup_pairs.tsv` 35 пар проверяют склейку относительного прилагательного с существительным (`зимние кроссовки` и `кроссовки на зиму`, `кожаный ремень` и `ремень из кожи`) и только 5 пар проверяют обычное словоизменение. Без `adj -> noun` метод `rulemma.lemma_key` показывает те же **22.2%**, а `rulemma.semantic_key` набирает **100.0% (FP=0)** и не склеивает 15 контрольных пар-ловушек (`умные часы` и `часы ум`, `мороженая рыба` и `мороженое рыба`, `ручной отпариватель` и `отпариватель для рук`, `соленые огурцы` и `соль огурцы`).
3. **Задержка одного вызова из Python.** При вызове по одному запросу в цикле Python `pymorphy3` с `lru_cache` (`5.6 мкс`) и `simplemma` (`8.0 мкс`) быстрее `rulemma` (`132.3 мкс`) из-за накладных расходов перехода `ctypes -> CGO`. Преимущество Go раскрывается на батчах (`lemmatize_all`, `lemmatize_batch`, `lemma_keys`, `semantic_keys`, `keys_batch`), в CLI и при вызове напрямую из Go.
4. **Кэш `pymorphy3`.** В тесте на 20 000 уникальных 7-словных запросов словарь слов ограничен `751` уникальными токенами, поэтому `lru_cache(200_000)` показывает высокий процент попаданий (`178 404 запр/с`). На реальных логах с миллионами уникальных опечаток и артикулов скорость `pymorphy3` падает в сторону режима без кэша (`2 532 запр/с`).

## Примеры работы слово в слово

| Входное слово | `rulemma 0.4.0` | `pymorphy3` | `mawo-pymorphy3` | `SteosMorphy` | `Natasha` | `MyStem` | `simplemma` |
|---|---|---|---|---|---|---|---|
| `спрей` | **спрей** | спреть | спря | **спрей** | **спрей** | **спрей** | **спрей** |
| `кофемашина` | **кофемашина** | кофемашин | **кофемашина** | **кофемашина** | кофемашин | **кофемашина** | **кофемашина** |
| `кружка` | **кружка** | **кружка** | **кружка** | **кружка** | кружок | **кружка** | кружок |
| `худи` | **худи** | худить | худить | худить | худь | худить | **худи** |
| `сяоми` | **сяоми** | сяомь | **сяоми** | сяомь | сяомь | сяомить | **сяоми** |
| `см` | **см** | смотреть | **см** | **см** | **см** | **см** | сантиметр |
| `мелки` | **мелок** | **мелок** | **мелок** | **мелок** | **мелок** | мелкий | мелкий |
| `оцинкованные` | **оцинкованный** | оцинковать | оцинковать | **оцинкованный** | оцинковать | оцинковывать | **оцинкованный** |
| `роботов-пылесосов` | **робот-пылесос** | **робот-пылесос** | **робот-пылесос** | роботов-пылесосов | **робот-пылесос** | **робот-пылесос** | роботов-пылесосов |
| `цветов` (омоним без контекста) | цвет | цвет | цвет | цвет | цвет | цветок | цветок |

## Когда лучше взять Natasha, MyStem, pymorphy3 или simplemma

`rulemma` не пытается заменить все NLP-инструменты сразу. Это бесконтекстный словарный движок под короткие поисковые запросы, товарные каталоги, SEO-кластеризацию и пакетные задачи на миллионы строк.

- Берите **[Natasha](https://github.com/natasha/natasha)** или **[Яндекс MyStem](https://yandex.ru/dev/mystem/)**, если разбираете связные предложения, новости и отзывы, где форма зависит от синтаксиса («они **стали** работать» vs «термос из нержавеющей **стали**»). Также у Natasha есть готовый NER и синтаксический разбор.
- Берите **[pymorphy3](https://github.com/no-plagiarism/pymorphy3)**, если нужны вероятностные предсказатели для редких несловарных OOV-глаголов или полный набор редких граммем OpenCorpora (вид, залог, наклонение, лицо). Для существительных, прилагательных, причастий, предложного управления и омонимов `rulemma 0.3` уже имеет встроенные `analyze`, `prep_governs`, `inflect` и `inflect_phrase`.
- Берите **[simplemma](https://github.com/adbar/simplemma)**, если проект мультиязычный (40+ языков) и нужен чистый Python без компилятора Go.
- Берите **`rulemma`**, если работаете с русскоязычными поисковыми запросами, каталогами и тегами, хотите убрать типовые ошибки OpenCorpora на брендах, единицах измерения, причастиях и омонимах, сохранить технические характеристики (`2,6 л.с.`, `100×100 см`, `б/у`, `m.2`), склеивать пары `прилагательное <-> существительное`, уложиться в ~37 МБ базового RSS (~16 МБ прироста в Python / ~12 МБ в Go) под MIT-лицензией и обрабатывать сотни тысяч и миллионы запросов в секунду.

## Как устроены словари и как их настроить под свой домен

Все данные хранятся в папке `data/` в сжатых TSV-файлах общим весом около `2.6 МБ`, с манифестом версий и контрольных сумм SHA-256 [`data/manifest.json`](data/manifest.json):

- `data/lemmas.tsv.gz`: `214 196` очищенных русских и товарных словоформ (`tools/build_dicts.py`: нормализация `3 755` причастий, ударных `-ой` прилагательных, защита 2–3-буквенных слов, дефисных городов РФ, чистка OOV-обрубков и нарицательных существительных).
- `data/adj_forms.tsv.gz`: `74 098` словоформ прилагательных и причастий для согласования по роду и числу.
- `data/types.tsv.gz`: `4 669` записей рода и числа для товарных типов (с поддержкой мн.ч. и составных названий в `TypeGram`).
- `data/adj_noun.tsv.gz`: `3 960` проверенных относительных пар `прилагательное -> существительное` (`tools/build_adj_noun.py`) с защитой от смыслового дрейфа.
- `data/morph.tsv.gz`: `127 163` известных словоформ с компактной 32-битной маской грамматических признаков (POS, род, число, падежи, одушевлённость, `Pltm`/`Sgtm`/`Fixd`; загружается лениво).
- `data/alt_lemmas.tsv.gz`: `11 909` реально омонимичных словоформ со всеми словарными разборами `(lemma, POS, cases)` (загружается лениво).
- `data/paradigms.tsv.gz`: `44 819` записей (`347` дедуплицированных суффиксных парадигм для `44 472` существительных, прилагательных и причастий; загружается лениво).
- `data/overrides.tsv`: `470` базовых ручных правок с наивысшим приоритетом.
- `data/manifest.json`: схема `schema_version: 1`, версия словаря, счётчики записей и SHA-256 хеши всех файлов.

Свой файл переопределений подключается без пересборки словарей через `--overrides custom.tsv` в CLI, `RuLemma(overrides="custom.tsv")` в Python или `Options{ExtraOverridesPath: "custom.tsv"}` в Go (см. [examples/10_ecommerce_overrides/](examples/10_ecommerce_overrides/)):

```tsv
# слово<TAB>лемма
ушм	болгарка
квадрокоптеры	дрон
```

## Примеры использования (`examples/01..13`)

В папке [`examples/`](examples/README.md) собраны 13 готовых проверяемых примеров (Python, Go и CLI), которые прогоняются в CI на каждый коммит: базовая лемматизация, сравнение со стеммингом, пакетная обработка 100 000 строк и TSV, миграция с `pymorphy3`, Go-горутины с `embedded.Load()`, препроцессинг, кастомные стоп-слова, частотный анализ и N-граммы, дедупликация запросов, кастомные `overrides.tsv`, сборка SEO-названий, подготовка корпуса под TF-IDF и новые возможности `v0.3` (технический профиль, морфоанализ и склонение).

## Структура репозитория

```text
rulemma/
├── version.go               # Version (0.4.0), ABIVersion (1), DictSchemaVersion (1)
├── arena.go                 # Компактные байтовые арены без GC-указателей (compactStrMap, compactU32Map, adjFormsTable) + hash/maphash
├── memfs.go                 # Потокобезопасная in-memory fs.FS (MemoryFS) для LoadMemory и динамической LoadTable (WASM / S3)
├── dict.go                  # Потоковый zero-alloc загрузчик .tsv.gz (os + io/fs), overrides.tsv, adj_noun и manifest.json
├── lemmatizer.go            # NormalizePhrase, Word, LookupWord, Lemmatize*, LemmaKey*, SemanticKey*, AdjToNoun, AgreeAdjective, BuildSEOName, LoadMemory, LoadTable
├── technical.go             # NormalizeHomoglyphs, TokenizeTechnical, KeyWithOptions, LemmatizeAllWithOptions (техпрофиль, связки предлогов, keep_words, compact_codes)
├── morph.go                 # Ленивый компактный морфоанализ: Analyze, WordCandidates, PrepGoverns
├── morph_infer.go           # Вывод морфологии для OOV-словоформ через paradigms.tsv.gz и составные основы (WordInfo.Inferred)
├── inflect.go               # Ленивое склонение и согласование: Inflect, InflectAdj, InflectPhrase, AllForms, AgreeNumber, Preposition
├── *_test.go                # Модульные, регрессионные, золотые (tests/golden/), race-тесты и Go-бенчмарк
├── embedded/                # Опциональный пакет //go:embed (2.6 МБ полностью или 1.2 МБ с -tags rulemma_core)
├── capi/capi.go             # Append-only C-shared библиотека (librulemma.so / .dylib, ABI v1)
├── cmd/rulemma/             # Параллельный CLI (version, info, lemmatize, tsv, bench, seoname, analyze, inflect) и CLI-тесты
├── cmd/rulemma-wasm/        # Референсный WebAssembly-модуль (тонкий 1.09 МБ gzip + core 2.29 МБ + full 3.63 МБ)
├── python/rulemma/          # Python-пакет без зависимостей (ctypes + fork-safety + CLI-бэкенд)
├── data/                    # Компактные словари (.tsv.gz), overrides.tsv, manifest.json, embed.go и embed_core.go
├── examples/                # 13 проверяемых примеров на Python, Go и Bash/CLI
├── tools/                   # Скрипты словарей и релизных проверок (build_dicts.py, build_adj_noun.py, update_manifest.py, release_check.py)
├── benchmarks/              # Открытые золотые наборы (.tsv) и воспроизводимое сравнение 8 библиотек
├── tests/                   # Тесты Python-биндингов, примеров и золотые регрессионные наборы (tests/golden/)
└── CHANGELOG.md             # История изменений (SemVer 2.0)
```

## Воспроизведение тестов, WASM и бенчмарков

```bash
# Сборка CLI и разделяемой библиотеки, проверка manifest.json, Go (-race), Python и всех examples/01..13
make build
make release-check
make test

# Сборка 3 профилей WebAssembly (thin 1.09 МБ / core 2.29 МБ / full 3.63 МБ gzip-9) в bin/wasm/
make build-wasm

# Воспроизведение сравнения 8 библиотек + масштабирования до 1M в отдельном venv
python3 -m venv .venv-bench
.venv-bench/bin/pip install "setuptools<81" pymorphy3 mawo-pymorphy3 steosmorphy natasha pymystem3 simplemma snowballstemmer
.venv-bench/bin/python benchmarks/compare_models.py --scale
```

## Авторы и лицензия

Авторы: **Коршиков Дмитрий и Gemini**. Официальный сайт проекта: [rulemma.ru](https://rulemma.ru/).

Код распространяется под лицензией **MIT**, подробности в файле [LICENSE](LICENSE). Словарная основа построена на открытых русских морфологических парадигмах [OpenCorpora](https://opencorpora.org/), очищенных автоматическими лингвистическими правилами (`tools/build_dicts.py`) и ручными переопределениями.
