# Примеры использования `rulemma`

В папке собраны два формата примеров:

1. **Jupyter-тетрадки с облачным HTTP API `rulemma.ru`** ([`notebooks/`](notebooks/README.md)) — запускаются в Google Colab или локальном Jupyter без установки Go и без сборки `librulemma.so`. Бесплатный API-ключ выпускается в личном кабинете [rulemma.ru/account/](https://rulemma.ru/account/) (базовые примеры работают и без ключа через песочницу `/v1/sandbox/test`).
2. **Локальные примеры (`01..13`)** на Go, Python CGO и CLI — не требуют сети и сторонних зависимостей, автоматически проверяются в CI (`make test-examples`).

---

## Jupyter-тетрадки (HTTP API `https://rulemma.ru/v1`)

- Регистрация и бесплатный API-ключ: [rulemma.ru/account/](https://rulemma.ru/account/)
- Документация API и тарифы: [rulemma.ru/api/](https://rulemma.ru/api/) · [rulemma.ru/pricing/](https://rulemma.ru/pricing/) · [openapi.yaml](https://rulemma.ru/openapi.yaml)

| Тетрадка | Методы API | Что показывает | Colab |
|---|---|---|---|
| [01 · Быстрый старт](notebooks/01_api_quickstart.ipynb) | `/health`, `/tariffs`, `/sandbox/test`, `/info`, `/usage`, `/homoglyphs` | Старт без ключа, авторизация, квота, омоглифы | [Открыть](https://colab.research.google.com/github/DimaKorshikov/rulemma/blob/main/examples/notebooks/01_api_quickstart.ipynb) |
| [02 · Дедупликация и техпрофиль](notebooks/02_query_dedup_and_technical_keys.ipynb) | `/lemmatize` | Кластеризация по `semantic_key`, `technical`, `overrides` | [Открыть](https://colab.research.google.com/github/DimaKorshikov/rulemma/blob/main/examples/notebooks/02_query_dedup_and_technical_keys.ipynb) |
| [03 · Морфология, склонение, SEO](notebooks/03_morphology_inflection_and_seo_titles.ipynb) | `/analyze`, `/inflect`, `/seoname` | Омонимы, OOV `inferred`, `prep_governs`, склонение, SEO | [Открыть](https://colab.research.google.com/github/DimaKorshikov/rulemma/blob/main/examples/notebooks/03_morphology_inflection_and_seo_titles.ipynb) |
| [04 · Частоты и TF-IDF](notebooks/04_text_frequency_and_tfidf_api.ipynb) | `/text-stats`, `/lemmatize` | Частотный словарь лемм, стоп-слова, корпус TF-IDF | [Открыть](https://colab.research.google.com/github/DimaKorshikov/rulemma/blob/main/examples/notebooks/04_text_frequency_and_tfidf_api.ipynb) |
| [05 · Батч CSV/TSV и ретраи](notebooks/05_batch_csv_pipeline_and_rate_limits.ipynb) | `/usage`, `/lemmatize` | Чанкинг по `max_batch`, ретраи `429`/`5xx`, контроль квоты | [Открыть](https://colab.research.google.com/github/DimaKorshikov/rulemma/blob/main/examples/notebooks/05_batch_csv_pipeline_and_rate_limits.ipynb) |

---

## Локальные примеры (`01..13`, без сети)

Перед запуском локальных примеров из корня репозитория выполните `make build`.

| Пример | Язык | Что показывает |
|---|---|---|
| [`01_lemmatization`](01_lemmatization/) | Python + Go | Базовая лемматизация слов и фраз, дефисные слова, `ё -> е`, `NormalizePhrase` |
| [`02_lemmatization_vs_stemming`](02_lemmatization_vs_stemming/) | Python | Разница между словарными леммами `rulemma` и усечёнными основами стеммера |
| [`03_batch_million_tsv`](03_batch_million_tsv/) | Python + CLI | Пакетная обработка `100 000` запросов в горутинах Go без GIL + потоковый CLI `tsv` |
| [`04_pymorphy3_migration`](04_pymorphy3_migration/) | Python | Миграция с `pymorphy2/3`: разбор 18+ проблемных слов (бренды, омонимы, единицы, причастия) |
| [`05_go_and_cli_pipeline`](05_go_and_cli_pipeline/) | Go + Bash | Параллельный Go-конвейер (`rulemma/embedded`) и stdin/stdout пайплайн CLI |
| [`06_text_preprocessing`](06_text_preprocessing/) | Python | Чистый препроцессинг русского текста (`normalize_phrase`, `keep_yo`, токены, леммы) |
| [`07_custom_stopwords`](07_custom_stopwords/) | Python + Go | Пользовательские стоп-слова в `lemma_key`, `semantic_key` и батчах |
| [`08_frequency_and_ngrams`](08_frequency_and_ngrams/) | Python | Частотный словарь лемм, водность/тошнота и биграммы/триграммы текста |
| [`09_query_dedup_clustering`](09_query_dedup_clustering/) | Python | Дедупликация поисковых запросов (`lemma_key`, `semantic_key`) и сопоставление с тегами |
| [`10_ecommerce_overrides`](10_ecommerce_overrides/) | Python + TSV | Подключение доменного `custom_overrides.tsv` без пересборки словарей |
| [`11_seo_title_builder`](11_seo_title_builder/) | Python + Go | Согласование прилагательных/причастий и сборка SEO-названий (`BuildSEOName`) |
| [`12_tfidf_with_rulemma`](12_tfidf_with_rulemma/) | Python | Подготовка корпуса через `lemmatize_batch` и расчёт TF-IDF (с рецептом для `scikit-learn`) |
| [`13_technical_morph_inflect`](13_technical_morph_inflect/) | Python | Профиль технических токенов (`v0.3`), связки предлогов, морфоанализ (`analyze`/`prep_governs`) и склонение (`inflect_phrase`/`agree_number`) |
