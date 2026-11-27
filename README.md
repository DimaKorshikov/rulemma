# rulemma

[![CI](https://github.com/DimaKorshikov/rulemma/actions/workflows/ci.yml/badge.svg)](https://github.com/DimaKorshikov/rulemma/actions/workflows/ci.yml)
[![Go Version](https://img.shields.io/badge/Go-1.21%2B-00ADD8?logo=go)](https://go.dev/)
[![Python](https://img.shields.io/badge/Python-3.9%2B-3776AB?logo=python)](https://www.python.org/)
[![License: MIT](https://img.shields.io/badge/License-MIT-green.svg)](LICENSE)

Словарный лемматизатор русского языка и поисковых e-commerce запросов на Go с нативными Python-биндингами (`ctypes` к `librulemma.so` / `.dylib`) без внешних зависимостей. Умеет лемматизировать слова и тексты, нормализовать фразы (`ё -> е`), строить канонические ключи с кастомными стоп-словами, сводить относительные прилагательные к существительным (`зимние кроссовки` и `кроссовки на зиму`) и согласовывать прилагательные и причастия с родом и числом названия товара.

Авторы: **Коршиков Дмитрий и Gemini**. Лицензия: **MIT**. История изменений: [CHANGELOG.md](CHANGELOG.md).

## Зачем появилась эта библиотека

Стандартные морфологические анализаторы русского языка создавались под художественную прозу и новости. Когда на вход поступают миллионы коротких поисковых запросов и карточек товаров, возникают три типичные проблемы:

1. Галлюцинации на новых словах, брендах в кириллице, единицах измерения и омонимах. Анализатор без контекста ошибочно преобразует `спрей` в `спреть`, `кофемашина` в `кофемашин`, `худи` в `худить`, `сяоми` в `сяомь`, `см` в `смотреть`, `витамин е` в `витамин быть`, а причастия вроде `оцинкованные` и `встраиваемые` — в глаголы `оцинковать`/`встраивать`.
2. Разъезжаются одинаковые по смыслу запросы. Обычный лемматизатор оставляет `зимние кроссовки` и `кроссовки на зиму`, `детский рюкзак` и `рюкзак для детей`, `школьная форма` и `форма в школу` в разных кластерах.
3. Производительность на больших массивах. В Python разбор десятков миллионов запросов через нейросетевые теггеры или внешние процессы упирается в тысячи запросов в секунду, GIL и сотни мегабайт RAM.

`rulemma` решает эти три задачи за счёт очищенного словаря (`204 839` словоформ, включая `3 755` причастий-определений и системную защиту нарицательных существительных и 2–3-буквенных сокращений), таблицы из `4 030` проверенных пар `прилагательное -> существительное`, `74 094` форм согласования, приоритетного файла ручных правок `data/overrides.tsv` (`383` записи) и пакетной обработки в горутинах Go.

## Быстрый старт на Python

Для сборки нативной библиотеки `librulemma.so` (или `.dylib` на macOS) нужны **Go 1.21+** и компилятор C (`gcc` или `clang`). В рантайме Python не требуется ни одного стороннего пакета.

```bash
git clone https://github.com/DimaKorshikov/rulemma.git
cd rulemma
make build            # собирает bin/rulemma и python/rulemma/librulemma.so
pip install --no-build-isolation .
```

```python
from rulemma import DEFAULT_STOP_WORDS, RuLemma, normalize_phrase

# Быстрая нормализация фразы (работает даже без загрузки словарей)
print(normalize_phrase("  Чёрная кофемашина, 35 см! "))               # "черная кофемашина 35 см"
print(normalize_phrase("  Чёрная кофемашина, 35 см! ", keep_yo=True)) # "чёрная кофемашина 35 см"

with RuLemma() as lem:
    print(lem.backend, lem.library_version, lem.abi_version)  # native 0.2.2 1

    # Лемматизация слов и фраз (буква ё нормализуется в е)
    print(lem.word("спрей"))                        # "спрей"
    print(lem.word("см"))                           # "см" (не "смотреть"!)
    print(lem.word("роботов-пылесосов"))            # "робот-пылесос"
    print(lem.lemmatize("Купить зимние кроссовки")) # "купить зимний кроссовок"

    # Канонический ключ (сортировка токенов, дедупликация, стандартные или кастомные стоп-слова)
    print(lem.lemma_key("Купить кроссовки Найк в Москве"))  # "кроссовок москва найк"
    print(
        lem.lemma_key(
            "Купить кроссовки Найк в Москве",
            stopwords=DEFAULT_STOP_WORDS | {"москва"},
        )
    )  # "кроссовок найк"

    # Семантический ключ со склейкой относительных прилагательных в существительные
    assert lem.semantic_key("зимние кроссовки найк") == lem.semantic_key("кроссовки на зиму найк")
    assert lem.semantic_key("детский рюкзак") == lem.semantic_key("рюкзак для детей")
    assert lem.semantic_key("школьная форма") == lem.semantic_key("форма в школу")

    # Параллельный батч в горутинах Go без блокировки Python GIL (вкл. кастомные stopwords=)
    queries = ["зимние кроссовки найк", "кроссовки на зиму найк", "кофемашина делонги"]
    print(lem.lemmatize_batch(queries))
    print(lem.lemma_keys(queries))
    print(lem.semantic_keys(queries))

    # Согласование прилагательных и причастий + сборка SEO-заголовка
    print(lem.agree_adjective("оцинкованные", "Ведро"))  # ("оцинкованное", True)
    print(lem.build_seo_name("Сумка кожаная", "Сумка", ["кожаный", "синие", "женские"], 100))
    # ("Сумка кожаная, синяя, женская", 2, 2)
```

Если вы обрабатываете тысячи и миллионы строк из Python, передавайте их списками в `lemmatize_batch`, `lemma_keys`, `semantic_keys`, `lemmatize_and_keys` или `normalize_batch`. Переход между `ctypes` и `CGO` выполняется один раз на весь батч, а обработка идёт параллельно на всех ядрах CPU.

## Быстрый старт на Go и CLI

Подключение как обычного Go-модуля (внешних зависимостей нет, потокобезопасность проверена через `go test -race ./...`):

```bash
go get github.com/DimaKorshikov/rulemma
```

В Go доступны два способа инициализации: загрузка словарей с диска (`rulemma.Load("data")`) или подключение одной строкой через опциональный субпакет `rulemma/embedded` (`//go:embed`, встраивает ~1.5 МБ сжатых словарей прямо в бинарник):

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
    fmt.Println(lem.LemmatizeString("Спрей от комаров"))          // спрей от комар
    fmt.Println(lem.LemmaKey("Купить кружки Сяоми"))              // кружка сяоми
    fmt.Println(lem.SemanticKey("зимние кроссовки найк"))         // зима кроссовок найк
    fmt.Println(lem.SemanticKey("кроссовки на зиму найк"))        // зима кроссовок найк

    customStop := rulemma.MakeStopWords([]string{"в", "москва"})
    fmt.Println(lem.LemmaKeyWithStop("Купить кроссовки в Москве", customStop)) // кроссовок купить

    form, _ := lem.AgreeAdjective("оцинкованные", "Ведро")
    fmt.Println(form) // оцинкованное
}
```

Для потоковой обработки крупных файлов есть бинарник `bin/rulemma`:

```bash
make build-cli

# Версия движка/ABI и статистика загруженных словарей + manifest.json
./bin/rulemma version
./bin/rulemma info --data data

# Поток через stdin (режимы: text | key | semkey | both | all | norm)
printf "зимние кроссовки найк\nкроссовки на зиму найк\n" | ./bin/rulemma lemmatize --mode semkey

# Кастомные стоп-слова (файл, список через запятую или 'none')
printf "Купить кроссовки в Москве\n" | ./bin/rulemma lemmatize --mode key --stopwords "в,москва"

# Параллельная обработка TSV-файла по заданной колонке
./bin/rulemma tsv --in queries.tsv --out queries_lem.tsv --col 0 --mode all --header --workers 16
```

## Сравнение с конкурентами и бенчмарк

Мы сравнили `rulemma 0.2.2` с семью популярными библиотеками для русского языка одним воспроизводимым скриптом [`benchmarks/compare_models.py`](benchmarks/compare_models.py). Каждая библиотека запускалась в **отдельном изолированном процессе** для честного замера пикового RSS RAM:

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
| **rulemma 0.2.2** (Go CGO) | Словарь + правила (Go, MIT) | **99.2%** | **100.0%** | **100.0% (FP=0)** | 172 мс | 104.1 МБ* | 128.5 мкс | **663 709 запр/с** |
| [pymorphy3](https://github.com/no-plagiarism/pymorphy3) (без кэша) | OpenCorpora FST (Python, MIT) | 94.7% | 41.7% | 18.2% (FP=0) | 47 мс | 45.4 МБ | 404.5 мкс | 2 471 запр/с |
| [pymorphy3](https://github.com/no-plagiarism/pymorphy3) (`lru_cache` 200k) | OpenCorpora FST + кэш (Python, MIT) | 94.7% | 41.7% | 18.2% (FP=0) | 47 мс | 45.4 МБ | **5.0 мкс** | 200 278 запр/с |
| [mawo-pymorphy3](https://pypi.org/project/mawo-pymorphy3/) 1.0.4 | Форк pymorphy3 (Python, MIT) | 81.2% | 76.7% | 14.0% (FP=0) | 20 мс | 39.8 МБ | 458.8 мкс | 2 167 запр/с |
| [SteosMorphy](https://github.com/SteosOfficial/SteosMorphy-py) 1.1.1 | Go DAWG + JSON ctypes (MIT) | 84.2% | 76.7% | 18.2% (FP=0) | 281 мс | 584.0 МБ | 3 407.5 мкс | 726 запр/с |
| [Natasha](https://github.com/natasha/natasha) ([Slovnet](https://github.com/natasha/slovnet)) | Нейросеть POS + OpenCorpora (MIT) | 95.5% | 74.2% | 13.6% (FP=1) | 2 430 мс | 234.2 МБ | 4 016.6 мкс | 248 запр/с |
| [simplemma](https://github.com/adbar/simplemma) (`ru`) | Словарь + правила (Python, MIT) | 93.2% | 90.0% | 14.0% (FP=0) | 2 743 мс | 162.8 МБ | 10.1 мкс | 98 939 запр/с |
| [pymystem3](https://github.com/nlpub/pymystem3) ([MyStem](https://yandex.ru/dev/mystem/)) | C++ бинарник (проприетарный) | 92.5% | 89.2% | 22.2% (FP=0) | 897 мс | 32.2 МБ† | 165.6 мкс | 5 314 запр/с |
| [SnowballStemmer](https://github.com/snowballstem/snowball) (`ru`) | Стеммер (Python, BSD-3) | 16.5%‡ | 49.2%‡ | 29.2% (FP=1) | **0 мс** | 21.8 МБ | 121.4 мкс | 8 236 запр/с |

*\*У `rulemma 0.2.2` сразу после загрузки словаря RSS процесса составляет `60.9 МБ` (включая интерпретатор Python); пик `104.1 МБ` возникает на параллельном батче из 20 000 строк. У `SteosMorphy` распакованный словарь `morph.dawg` занимает `445 МБ` на диске против `1.7 МБ` у `rulemma`.*  
*†В `pymystem3` бинарник `mystem` работает в отдельном дочернем процессе, поэтому `RUSAGE_SELF` показывает только Python-обёртку.*  
*‡`SnowballStemmer` возвращает усечённые основы, а не словарные леммы.*

На масштабировании из Python (7-словные запросы) `rulemma 0.2.2` обрабатывает **1 000 000 запросов за `1.18 с`** (`844 953 запр/с` для `lemmatize_batch` и **`857 423 запр/с`** для `semantic_keys`). В чистом Go (`go test -bench=BenchmarkLemmaKey -benchmem .`) один вызов `LemmaKey` требует `3 896 нс` (`3.9 мкс`), `664 Б` памяти и `20` аллокаций.

## Открытые оговорки по методике замера

1. **Авторство тестовых наборов.** Все три файла ([`benchmarks/gold_general_ru.tsv`](benchmarks/gold_general_ru.tsv), [`benchmarks/gold_ecommerce_ru.tsv`](benchmarks/gold_ecommerce_ru.tsv), [`benchmarks/gold_dedup_pairs.tsv`](benchmarks/gold_dedup_pairs.tsv)) подготовлены авторами `rulemma`. В `v0.2.1` мы закрыли 5 реальных словарных промахов, обнаруженных при выпуске `v0.2.0` (`отпариватели`, `дочерей`, `воробьев`, `автодорог`, `большие`), и зафиксировали все изменения в [CHANGELOG.md](CHANGELOG.md) и золотых тестах [`tests/golden/`](tests/golden/). Единственным оставшимся расхождением на `gold_general_ru.tsv` (`132/133`, `99.2%`) остаётся честный бесконтекстный омоним `цветов -> цвет` (в тесте ожидается `цветок`).
2. **Почему в колонке дедупликации F1 у конкурентов 13.6% до 22.2%.** Из 40 положительных пар в `gold_dedup_pairs.tsv` 35 пар проверяют склейку относительного прилагательного с существительным (`зимние кроссовки` и `кроссовки на зиму`, `кожаный ремень` и `ремень из кожи`) и только 5 пар проверяют обычное словоизменение. Без `adj -> noun` метод `rulemma.lemma_key` показывает те же **22.2%**, а `rulemma.semantic_key` набирает **100.0% (FP=0)** и не склеивает 15 контрольных пар-ловушек (`умные часы` и `часы ум`, `мороженая рыба` и `мороженое рыба`, `ручной отпариватель` и `отпариватель для рук`, `соленые огурцы` и `соль огурцы`).
3. **Задержка одного вызова из Python.** При вызове по одному запросу в цикле Python `simplemma` (`5.1 мкс`) и `pymorphy3` с `lru_cache` (`5.7 мкс`) быстрее `rulemma` (`128.3 мкс`) из-за накладных расходов перехода `ctypes -> CGO`. Преимущество Go раскрывается на батчах (`lemmatize_batch`, `lemma_keys`, `semantic_keys`), в CLI и при вызове напрямую из Go.
4. **Кэш `pymorphy3`.** В тесте на 20 000 сгенерированных запросов словарь слов ограничен, поэтому `lru_cache(200_000)` показывает высокий процент попаданий (`175 811 запр/с`). На реальных логах с миллионами уникальных опечаток и артикулов скорость `pymorphy3` падает в сторону режима без кэша (`2 670 запр/с`).

## Примеры работы слово в слово

| Входное слово | `rulemma 0.2.2` | `pymorphy3` | `mawo-pymorphy3` | `SteosMorphy` | `Natasha` | `MyStem` | `simplemma` |
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
- Берите **[pymorphy3](https://github.com/no-plagiarism/pymorphy3)**, если нужны полные грамматические теги (часть речи, падеж, число, род, одушевлённость) и склонение произвольных слов по падежам.
- Берите **[simplemma](https://github.com/adbar/simplemma)**, если проект мультиязычный (40+ языков) и нужен чистый Python без компилятора Go.
- Берите **`rulemma`**, если работаете с русскоязычными поисковыми запросами, каталогами и тегами, хотите убрать типовые ошибки OpenCorpora на брендах, единицах измерения, причастиях и омонимах, склеивать пары `прилагательное <-> существительное`, уложиться в ~60 МБ RAM под MIT-лицензией и обрабатывать сотни тысяч и миллионы запросов в секунду.

## Как устроены словари и как их настроить под свой домен

Все данные хранятся в папке `data/` в сжатых TSV-файлах общим весом около `1.7 МБ`, с манифестом версий и контрольных сумм SHA-256 [`data/manifest.json`](data/manifest.json):

- `data/lemmas.tsv.gz`: `204 839` очищенных русских и товарных словоформ (исходная база OpenCorpora пропущена через скрипт чистки `tools/build_dicts.py` с системной нормализацией `3 755` причастий и защитой 2–3-буквенных слов).
- `data/adj_forms.tsv.gz`: `74 094` словоформы прилагательных и причастий для согласования по роду и числу.
- `data/types.tsv.gz`: `4 662` записи рода и числа для товарных типов.
- `data/adj_noun.tsv.gz`: `4 030` проверенных относительных пар `прилагательное -> существительное` (`tools/build_adj_noun.py`) с защитой от смыслового дрейфа.
- `data/overrides.tsv`: `383` базовые ручные правки с наивысшим приоритетом.
- `data/manifest.json`: схема `schema_version: 1`, версия словаря, счётчики записей и SHA-256 хеши всех файлов.

Свой файл переопределений подключается без пересборки словарей через `--overrides custom.tsv` в CLI, `RuLemma(overrides="custom.tsv")` в Python или `Options{ExtraOverridesPath: "custom.tsv"}` в Go (см. [examples/10_ecommerce_overrides/](examples/10_ecommerce_overrides/)):

```tsv
# слово<TAB>лемма
ушм	болгарка
квадрокоптеры	дрон
```

## Примеры использования (`examples/01..12`)

В папке [`examples/`](examples/README.md) собраны 12 готовых проверяемых примеров (Python, Go и CLI), которые прогоняются в CI на каждый коммит: базовая лемматизация, сравнение со стеммингом, пакетная обработка 100 000 строк и TSV, миграция с `pymorphy3`, Go-горутины с `embedded.Load()`, препроцессинг, кастомные стоп-слова, частотный анализ и N-граммы, дедупликация запросов, кастомные `overrides.tsv`, сборка SEO-названий и подготовка корпуса под TF-IDF.

## Структура репозитория

```text
rulemma/
├── version.go               # Version (0.2.2), ABIVersion (1), DictSchemaVersion (1)
├── dict.go                  # Загрузчик .tsv.gz (os + io/fs), overrides.tsv, adj_noun и manifest.json
├── lemmatizer.go            # NormalizePhrase, Word, Lemmatize, LemmaKey*, SemanticKey*, AdjToNoun, AgreeAdjective, BuildSEOName
├── lemmatizer_test.go       # Модульные, регрессионные, золотые (tests/golden/), race-тесты и Go-бенчмарк
├── embedded/                # Опциональный пакет //go:embed для инициализации одной строкой (embedded.Load())
├── capi/capi.go             # Append-only C-shared библиотека (librulemma.so / .dylib, ABI v1)
├── cmd/rulemma/             # Параллельный CLI (version, info, lemmatize, tsv, bench, seoname) и CLI-тесты
├── python/rulemma/          # Python-пакет без зависимостей (ctypes + CLI-бэкенд)
├── data/                    # Компактные словари (.tsv.gz), overrides.tsv, manifest.json и embed.go
├── examples/                # 12 проверяемых примеров на Python, Go и Bash/CLI
├── tools/                   # Скрипты пересборки словарей (build_dicts.py, build_adj_noun.py, update_manifest.py)
├── benchmarks/              # Открытые золотые наборы (.tsv) и воспроизводимое сравнение 8 библиотек
├── tests/                   # Тесты Python-биндингов, примеров и золотые регрессионные наборы (tests/golden/)
└── CHANGELOG.md             # История изменений (SemVer 2.0)
```

## Воспроизведение тестов и бенчмарков

```bash
# Сборка CLI и разделяемой библиотеки, проверка manifest.json, Go (-race), Python и всех examples/01..12
make build
make test

# Воспроизведение сравнения 8 библиотек + масштабирования до 1M в отдельном venv
python3 -m venv .venv-bench
.venv-bench/bin/pip install "setuptools<81" pymorphy3 mawo-pymorphy3 steosmorphy natasha pymystem3 simplemma snowballstemmer
.venv-bench/bin/python benchmarks/compare_models.py --scale
```

## Авторы и лицензия

Авторы: **Коршиков Дмитрий и Gemini**.

Код распространяется под лицензией **MIT**, подробности в файле [LICENSE](LICENSE). Словарная основа построена на открытых русских морфологических парадигмах [OpenCorpora](https://opencorpora.org/), очищенных автоматическими лингвистическими правилами (`tools/build_dicts.py`) и ручными переопределениями.
