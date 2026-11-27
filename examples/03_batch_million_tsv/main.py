"""03. Пакетная лемматизация 100 000 запросов в горутинах Go без GIL + потоковый CLI tsv."""
from __future__ import annotations

import tempfile
import time
from pathlib import Path

from rulemma import RuLemma


def main() -> None:
    base = [
        "купить зимние кроссовки найк мужские черные",
        "кроссовки на зиму найк мужские черные недорого",
        "спрей от комаров детский 100 мл",
        "кофемашина делонги автоматическая с капучинатором",
        "встраиваемые посудомоечные машины 60 см",
    ]
    queries = [f"{base[i % len(base)]} модель {i}" for i in range(100_000)]

    with RuLemma() as lem:
        t0 = time.perf_counter()
        keys = lem.semantic_keys(queries)
        dt = time.perf_counter() - t0
        assert len(keys) == len(queries)
        assert lem.semantic_key(base[0]) == lem.semantic_key(base[1])
        print(f"[Python CGO batch] {len(queries):,} запросов за {dt:.3f} с ({len(queries) / dt:,.0f} запр/с)")

        # Потоковая обработка TSV-файла напрямую через CLI (если бинарник собран)
        if lem.binary:
            with tempfile.TemporaryDirectory() as td:
                in_tsv = Path(td) / "queries.tsv"
                out_tsv = Path(td) / "queries_out.tsv"
                in_tsv.write_text(
                    "query\tfreq\n" + "\n".join(f"{q}\t{100 + i}" for i, q in enumerate(queries[:10_000])) + "\n",
                    encoding="utf-8",
                )
                stat_line = lem.lemmatize_tsv(in_tsv, out_tsv, col=0, mode="all", header=True)
                header = out_tsv.read_text(encoding="utf-8").splitlines()[0]
                assert header == "query\tfreq\tlemma_text\tlemma_key\tsemantic_key", header
                print("[Go CLI tsv]", stat_line)


if __name__ == "__main__":
    main()
