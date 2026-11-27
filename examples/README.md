# Примеры использования `rulemma`

Все примеры самодостаточны, не требуют сторонних зависимостей сверх `rulemma` (Go stdlib + Python stdlib) и автоматически проверяются в CI (`make test-examples`).

Перед запуском из корня репозитория выполните `make build`.

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
