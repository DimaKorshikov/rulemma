# Jupyter-тетрадки с HTTP API `rulemma.ru`

Готовые интерактивные тетрадки для Google Colab и локального Jupyter. Не требуют установки Go, сборки нативной библиотеки или сторонних pip-пакетов — все запросы к `https://rulemma.ru/v1` работают на стандартной библиотеке Python (`urllib.request`).

### Регистрация и бесплатный API-ключ

- Регистрация и выпуск токена `rlm_live_...`: [rulemma.ru/account/](https://rulemma.ru/account/) (вход по email, Яндекс ID или Google)
- Документация эндпоинтов: [rulemma.ru/api/](https://rulemma.ru/api/) · OpenAPI: [openapi.yaml](https://rulemma.ru/openapi.yaml)
- Актуальные тарифы и квоты: [rulemma.ru/pricing/](https://rulemma.ru/pricing/)

Передайте токен через переменную окружения `RULEMMA_API_KEY` (в Google Colab удобно хранить его в панели `Secrets`). Токен всегда отправляется в заголовке `Authorization: Bearer ...` и никогда не передаётся в URL. Без ключа базовые примеры автоматически запускаются через публичную песочницу `POST /v1/sandbox/test` (до 10 строк за вызов).

### Список тетрадок

| Тетрадка | Эндпоинты API | Что разбирает | Colab |
|---|---|---|---|
| [01 · Быстрый старт](01_api_quickstart.ipynb) | `/health`, `/tariffs`, `/sandbox/test`, `/info`, `/usage`, `/homoglyphs` | Старт без ключа, авторизация, квота, омоглифы | [Открыть](https://colab.research.google.com/github/DimaKorshikov/rulemma/blob/main/examples/notebooks/01_api_quickstart.ipynb) |
| [02 · Дедупликация и техпрофиль](02_query_dedup_and_technical_keys.ipynb) | `/lemmatize` | Кластеризация по `semantic_key`, `technical`, `overrides` | [Открыть](https://colab.research.google.com/github/DimaKorshikov/rulemma/blob/main/examples/notebooks/02_query_dedup_and_technical_keys.ipynb) |
| [03 · Морфология, склонение, SEO](03_morphology_inflection_and_seo_titles.ipynb) | `/analyze`, `/inflect`, `/seoname` | Омонимы, OOV `inferred`, `prep_governs`, склонение, SEO | [Открыть](https://colab.research.google.com/github/DimaKorshikov/rulemma/blob/main/examples/notebooks/03_morphology_inflection_and_seo_titles.ipynb) |
| [04 · Частоты и TF-IDF](04_text_frequency_and_tfidf_api.ipynb) | `/text-stats`, `/lemmatize` | Частотный словарь лемм, стоп-слова, корпус TF-IDF | [Открыть](https://colab.research.google.com/github/DimaKorshikov/rulemma/blob/main/examples/notebooks/04_text_frequency_and_tfidf_api.ipynb) |
| [05 · Батч CSV/TSV и ретраи](05_batch_csv_pipeline_and_rate_limits.ipynb) | `/usage`, `/lemmatize` | Чанкинг по `max_batch`, ретраи `429`/`5xx`, контроль квоты | [Открыть](https://colab.research.google.com/github/DimaKorshikov/rulemma/blob/main/examples/notebooks/05_batch_csv_pipeline_and_rate_limits.ipynb) |

### Локальный запуск

```bash
export RULEMMA_API_KEY="rlm_live_ваш_ключ"
jupyter notebook examples/notebooks/
```

Для обработки миллионов строк локально без сети используйте Go / Python CGO / CLI примеры из [examples/README.md](../README.md).
