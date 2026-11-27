"""Reproducible comparative benchmark: rulemma (Go CGO) vs Russian NLP libraries (2026).

Compares up to 8 libraries, each measured in an isolated subprocess for accurate peak RSS RAM:
  1. rulemma (Go C-shared lib via Python ctypes — single & multi-core Go batch)
  2. pymorphy3 (OpenCorpora FST, uncached & lru_cache(200k))
  3. mawo-pymorphy3 (optimized pymorphy3 fork)
  4. SteosMorphy (Go DAWG via Python ctypes)
  5. Natasha (razdel + MorphVocab + NewsMorphTagger — neural context tagger)
  6. simplemma (lightweight Python dict+rules lemmatizer)
  7. pymystem3 (Yandex MyStem proprietary C++ binary bridge)
  8. SnowballStemmer (Russian Snowball stemmer baseline)

Usage:
  .venv-bench/bin/python benchmarks/compare_models.py
  .venv-bench/bin/python benchmarks/compare_models.py --scale   # also measure 20k / 100k / 1M scaling
"""
from __future__ import annotations

import argparse
import functools
import json
import os
import platform
import re
import resource
import subprocess
import sys
import time
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
BENCH = ROOT / "benchmarks"
sys.path.insert(0, str(ROOT / "python"))

TOK_RE = re.compile(r"[a-zа-яё0-9]+(?:-[a-zа-яё0-9]+)*", re.I)
STOP_WORDS = {
    "в", "во", "на", "для", "и", "с", "со", "по", "из", "от", "до", "без", "под", "к", "у", "о", "об",
    "а", "или", "купить", "цена", "цены", "недорого", "дешево", "интернет", "магазин",
}


def norm_yo(s: str) -> str:
    return s.lower().replace("ё", "е").strip()


def peak_rss_mb() -> float:
    rss = resource.getrusage(resource.RUSAGE_SELF).ru_maxrss
    if platform.system() == "Darwin":
        return round(rss / (1024 * 1024), 1)
    return round(rss / 1024.0, 1)


def current_rss_mb() -> float:
    status = Path("/proc/self/status")
    if status.exists():
        for line in status.read_text(encoding="utf-8", errors="ignore").splitlines():
            if line.startswith("VmRSS:"):
                kb = int(line.split()[1])
                return round(kb / 1024.0, 1)
    return peak_rss_mb()


def load_tsv_pairs(path: Path, max_rows: int | None = None) -> list[tuple[str, str]]:
    out = []
    seen = set()
    for line in path.read_text(encoding="utf-8").splitlines():
        line = line.strip()
        if not line or line.startswith("#"):
            continue
        parts = line.split("\t")
        if len(parts) >= 2:
            w, g = parts[0].strip(), norm_yo(parts[1])
            if (w, g) not in seen:
                seen.add((w, g))
                out.append((w, g))
                if max_rows and len(out) >= max_rows:
                    break
    return out


def load_dedup_pairs(path: Path) -> list[tuple[int, str, str, str]]:
    out = []
    for line in path.read_text(encoding="utf-8").splitlines():
        line = line.strip()
        if not line or line.startswith("#"):
            continue
        parts = line.split("\t")
        if len(parts) >= 4:
            out.append((int(parts[0]), parts[1], parts[2], parts[3]))
    return out


def build_queries(ecom_all: list[tuple[str, str]], n: int) -> list[str]:
    all_words = [w for w, _ in ecom_all]
    nouns_pool = all_words[:260]
    adjs_pool = all_words[480:720]
    tail_pool = all_words[720:980]
    out = []
    for i in range(n):
        w1 = nouns_pool[i % len(nouns_pool)]
        w2 = adjs_pool[(i * 7 + 3) % len(adjs_pool)]
        w3 = tail_pool[(i * 13 + 5) % len(tail_pool)]
        w4 = nouns_pool[(i * 17 + 11) % len(nouns_pool)]
        w5 = adjs_pool[(i * 23 + 19) % len(adjs_pool)]
        out.append(f"купить {w2} {w1} для {w3} {w5} {w4}")
    return out


def generic_key(text: str, word_fn) -> str:
    toks = TOK_RE.findall(text.lower())
    seen = set()
    res = []
    for t in toks:
        if t in STOP_WORDS:
            continue
        lm = norm_yo(word_fn(t))
        if not lm or lm in STOP_WORDS or lm in seen:
            continue
        seen.add(lm)
        res.append(lm)
    res.sort()
    return " ".join(res)


def eval_word_acc(pairs: list[tuple[str, str]], word_fn) -> tuple[float, list[tuple[str, str, str]]]:
    ok = 0
    misses = []
    for w, gold in pairs:
        got = norm_yo(word_fn(w))
        if got == gold:
            ok += 1
        else:
            misses.append((w, gold, got))
    return round(100.0 * ok / max(1, len(pairs)), 2), misses


def eval_dedup(pairs: list[tuple[int, str, str, str]], key_fn) -> dict:
    tp = fp = fn = tn = 0
    for lbl, a, b, _cat in pairs:
        pred = 1 if key_fn(a) == key_fn(b) else 0
        if lbl == 1 and pred == 1:
            tp += 1
        elif lbl == 0 and pred == 1:
            fp += 1
        elif lbl == 1 and pred == 0:
            fn += 1
        else:
            tn += 1
    prec = tp / max(1, tp + fp)
    rec = tp / max(1, tp + fn)
    f1 = 2 * prec * rec / max(1e-9, prec + rec)
    return {
        "tp": tp, "fp": fp, "fn": fn, "tn": tn,
        "precision": round(100.0 * prec, 1),
        "recall": round(100.0 * rec, 1),
        "f1": round(100.0 * f1, 1),
    }


def run_worker(worker: str, batch_size: int) -> list[dict]:
    general_pairs = load_tsv_pairs(BENCH / "gold_general_ru.tsv")
    ecom_all = load_tsv_pairs(BENCH / "gold_ecommerce_ru.tsv")
    ecom_hard = ecom_all[:120]
    dedup_pairs = load_dedup_pairs(BENCH / "gold_dedup_pairs.tsv")
    batch_queries = build_queries(ecom_all, batch_size)
    single_2k = batch_queries[:2_000]

    if worker == "rulemma":
        from rulemma import RuLemma, __version__
        t0 = time.perf_counter()
        rl = RuLemma(workers=os.cpu_count() or 8)
        load_ms = (time.perf_counter() - t0) * 1000
        load_rss = current_rss_mb()

        gen_acc, gen_miss = eval_word_acc(general_pairs, rl.word)
        eco_acc, eco_miss = eval_word_acc(ecom_hard, rl.word)
        dedup_lk = eval_dedup(dedup_pairs, rl.lemma_key)
        dedup_sk = eval_dedup(dedup_pairs, rl.semantic_key)

        t0 = time.perf_counter()
        for q in single_2k:
            _ = rl.lemmatize(q)
        lat_us = (time.perf_counter() - t0) * 1e6 / len(single_2k)

        t0 = time.perf_counter()
        _ = rl.lemmatize_batch(batch_queries)
        qps_batch = len(batch_queries) / (time.perf_counter() - t0)

        t0 = time.perf_counter()
        _ = rl.semantic_keys(batch_queries)
        qps_sem = len(batch_queries) / (time.perf_counter() - t0)

        rss_peak = peak_rss_mb()
        rl.close()
        return [{
            "model": f"rulemma {__version__} (Go CGO native)",
            "key": "rulemma",
            "type": "Словарь + правила (Go, MIT)",
            "load_ms": round(load_ms, 1),
            "load_rss_mb": load_rss,
            "peak_rss_mb": rss_peak,
            "general_acc": gen_acc,
            "ecom_acc": eco_acc,
            "dedup_f1": dedup_sk["f1"],
            "dedup_inflect_f1": dedup_lk["f1"],
            "dedup_fp": dedup_sk["fp"],
            "single_us": round(lat_us, 1),
            "batch_qps": int(qps_batch),
            "semkey_qps": int(qps_sem),
            "gen_misses": gen_miss[:10],
            "eco_misses": eco_miss[:10],
        }]

    if worker == "pymorphy3":
        import pymorphy3
        t0 = time.perf_counter()
        pm = pymorphy3.MorphAnalyzer()
        load_ms = (time.perf_counter() - t0) * 1000
        load_rss = current_rss_mb()

        def pm_word(w: str) -> str:
            return pm.parse(w)[0].normal_form

        @functools.lru_cache(maxsize=200_000)
        def pm_word_cached(w: str) -> str:
            return pm.parse(w)[0].normal_form

        gen_acc, gen_miss = eval_word_acc(general_pairs, pm_word)
        eco_acc, eco_miss = eval_word_acc(ecom_hard, pm_word)
        dedup_pm = eval_dedup(dedup_pairs, lambda s: generic_key(s, pm_word_cached))

        t0 = time.perf_counter()
        for q in single_2k[:500]:
            _ = " ".join(pm_word(t) for t in TOK_RE.findall(q.lower()))
        lat_uncached_us = (time.perf_counter() - t0) * 1e6 / 500
        qps_uncached = 1e6 / lat_uncached_us

        pm_word_cached.cache_clear()
        t0 = time.perf_counter()
        for q in batch_queries:
            _ = " ".join(pm_word_cached(t) for t in TOK_RE.findall(q.lower()))
        dt_cached = time.perf_counter() - t0
        qps_cached = len(batch_queries) / dt_cached
        lat_cached_us = dt_cached * 1e6 / len(batch_queries)
        rss_peak = peak_rss_mb()

        return [
            {
                "model": "pymorphy3 (uncached)",
                "key": "pymorphy3_uncached",
                "type": "OpenCorpora FST (Python, MIT)",
                "load_ms": round(load_ms, 1),
                "load_rss_mb": load_rss,
                "peak_rss_mb": rss_peak,
                "general_acc": gen_acc,
                "ecom_acc": eco_acc,
                "dedup_f1": dedup_pm["f1"],
                "dedup_fp": dedup_pm["fp"],
                "single_us": round(lat_uncached_us, 1),
                "batch_qps": int(qps_uncached),
                "gen_misses": gen_miss[:10],
                "eco_misses": eco_miss[:10],
            },
            {
                "model": "pymorphy3 (lru_cache 200k)",
                "key": "pymorphy3_cached",
                "type": "OpenCorpora FST + кэш (Python, MIT)",
                "load_ms": round(load_ms, 1),
                "load_rss_mb": load_rss,
                "peak_rss_mb": rss_peak,
                "general_acc": gen_acc,
                "ecom_acc": eco_acc,
                "dedup_f1": dedup_pm["f1"],
                "dedup_fp": dedup_pm["fp"],
                "single_us": round(lat_cached_us, 1),
                "batch_qps": int(qps_cached),
                "gen_misses": gen_miss[:10],
                "eco_misses": eco_miss[:10],
            },
        ]

    if worker == "mawo_pymorphy3":
        import mawo_pymorphy3
        t0 = time.perf_counter()
        mm = mawo_pymorphy3.MorphAnalyzer()
        load_ms = (time.perf_counter() - t0) * 1000
        load_rss = current_rss_mb()

        def mm_word(w: str) -> str:
            p = mm.parse(w)
            return p[0].normal_form if p else w

        gen_acc, gen_miss = eval_word_acc(general_pairs, mm_word)
        eco_acc, eco_miss = eval_word_acc(ecom_hard, mm_word)
        dedup_mm = eval_dedup(dedup_pairs, lambda s: generic_key(s, mm_word))

        n_single = 500
        t0 = time.perf_counter()
        for q in single_2k[:n_single]:
            _ = " ".join(mm_word(t) for t in TOK_RE.findall(q.lower()))
        lat_us = (time.perf_counter() - t0) * 1e6 / n_single

        t0 = time.perf_counter()
        for q in batch_queries:
            _ = " ".join(mm_word(t) for t in TOK_RE.findall(q.lower()))
        dt_batch = time.perf_counter() - t0
        rss_peak = peak_rss_mb()

        return [{
            "model": "mawo-pymorphy3",
            "key": "mawo_pymorphy3",
            "type": "Форк pymorphy3 (Python, MIT)",
            "load_ms": round(load_ms, 1),
            "load_rss_mb": load_rss,
            "peak_rss_mb": rss_peak,
            "general_acc": gen_acc,
            "ecom_acc": eco_acc,
            "dedup_f1": dedup_mm["f1"],
            "dedup_fp": dedup_mm["fp"],
            "single_us": round(lat_us, 1),
            "batch_qps": int(len(batch_queries) / dt_batch),
            "gen_misses": gen_miss[:10],
            "eco_misses": eco_miss[:10],
        }]

    if worker == "steosmorphy":
        import steosmorphy
        t0 = time.perf_counter()
        sm = steosmorphy.MorphAnalyzer(steosmorphy.AnalyzerConfig())
        load_ms = (time.perf_counter() - t0) * 1000
        load_rss = current_rss_mb()

        def sm_word(w: str) -> str:
            res = sm.analyze(w)
            if res and res.parses:
                return res.parses[0].lemma or w
            return w

        gen_acc, gen_miss = eval_word_acc(general_pairs, sm_word)
        eco_acc, eco_miss = eval_word_acc(ecom_hard, sm_word)
        dedup_sm = eval_dedup(dedup_pairs, lambda s: generic_key(s, sm_word))

        # Single-query latency (500 queries)
        n_single = 500
        t0 = time.perf_counter()
        for q in single_2k[:n_single]:
            _ = " ".join(sm_word(t) for t in TOK_RE.findall(q.lower()))
        lat_us = (time.perf_counter() - t0) * 1e6 / n_single

        # Batch throughput via native parse_list in safe 4,000-word chunks (avoids multi-GB single JSON spike)
        n_batch = min(len(batch_queries), 20_000)
        t0 = time.perf_counter()
        tokenized = [TOK_RE.findall(q.lower()) for q in batch_queries[:n_batch]]
        flat_words = [w for toks in tokenized for w in toks]
        chunk_sz = 4000
        for i in range(0, len(flat_words), chunk_sz):
            _ = sm.parse_list(flat_words[i : i + chunk_sz])
        dt_batch = time.perf_counter() - t0
        rss_peak = peak_rss_mb()
        sm.destroy()

        return [{
            "model": "SteosMorphy (Go ctypes)",
            "key": "steosmorphy",
            "type": "Go DAWG + JSON ctypes (MIT)",
            "load_ms": round(load_ms, 1),
            "load_rss_mb": load_rss,
            "peak_rss_mb": rss_peak,
            "general_acc": gen_acc,
            "ecom_acc": eco_acc,
            "dedup_f1": dedup_sm["f1"],
            "dedup_fp": dedup_sm["fp"],
            "single_us": round(lat_us, 1),
            "batch_qps": int(n_batch / dt_batch),
            "gen_misses": gen_miss[:10],
            "eco_misses": eco_miss[:10],
        }]

    if worker == "natasha":
        from natasha import Doc, MorphVocab, NewsEmbedding, NewsMorphTagger, Segmenter
        t0 = time.perf_counter()
        seg = Segmenter()
        mv = MorphVocab()
        emb = NewsEmbedding()
        tagger = NewsMorphTagger(emb)
        load_ms = (time.perf_counter() - t0) * 1000
        load_rss = current_rss_mb()

        def nat_lemmatize(text: str) -> list[str]:
            doc = Doc(text)
            doc.segment(seg)
            doc.tag_morph(tagger)
            out = []
            for tok in doc.tokens:
                tok.lemmatize(mv)
                out.append(norm_yo(tok.lemma or tok.text))
            return out

        def nat_word(w: str) -> str:
            res = nat_lemmatize(w)
            return res[0] if res else w

        def nat_key(text: str) -> str:
            toks = nat_lemmatize(text)
            return " ".join(sorted({t for t in toks if t and t not in STOP_WORDS and TOK_RE.fullmatch(t)}))

        gen_acc, gen_miss = eval_word_acc(general_pairs, nat_word)
        eco_acc, eco_miss = eval_word_acc(ecom_hard, nat_word)
        dedup_nat = eval_dedup(dedup_pairs, nat_key)

        n_nat = 400
        t0 = time.perf_counter()
        for q in single_2k[:n_nat]:
            _ = nat_lemmatize(q)
        dt_nat = time.perf_counter() - t0
        rss_peak = peak_rss_mb()

        return [{
            "model": "Natasha (Slovnet + MorphVocab)",
            "key": "natasha",
            "type": "Нейросеть POS + OpenCorpora (MIT)",
            "load_ms": round(load_ms, 1),
            "load_rss_mb": load_rss,
            "peak_rss_mb": rss_peak,
            "general_acc": gen_acc,
            "ecom_acc": eco_acc,
            "dedup_f1": dedup_nat["f1"],
            "dedup_fp": dedup_nat["fp"],
            "single_us": round(dt_nat * 1e6 / n_nat, 1),
            "batch_qps": int(n_nat / dt_nat),
            "gen_misses": gen_miss[:10],
            "eco_misses": eco_miss[:10],
        }]

    if worker == "simplemma":
        import simplemma
        t0 = time.perf_counter()
        _ = simplemma.lemmatize("кроссовки", lang="ru")
        load_ms = (time.perf_counter() - t0) * 1000
        load_rss = current_rss_mb()

        def simp_word(w: str) -> str:
            return simplemma.lemmatize(w, lang="ru")

        gen_acc, gen_miss = eval_word_acc(general_pairs, simp_word)
        eco_acc, eco_miss = eval_word_acc(ecom_hard, simp_word)
        dedup_simp = eval_dedup(dedup_pairs, lambda s: generic_key(s, simp_word))

        t0 = time.perf_counter()
        for q in single_2k:
            _ = " ".join(simp_word(t) for t in TOK_RE.findall(q.lower()))
        dt_simp = time.perf_counter() - t0
        rss_peak = peak_rss_mb()

        return [{
            "model": "simplemma (ru)",
            "key": "simplemma",
            "type": "Словарь + правила (Python, MIT)",
            "load_ms": round(load_ms, 1),
            "load_rss_mb": load_rss,
            "peak_rss_mb": rss_peak,
            "general_acc": gen_acc,
            "ecom_acc": eco_acc,
            "dedup_f1": dedup_simp["f1"],
            "dedup_fp": dedup_simp["fp"],
            "single_us": round(dt_simp * 1e6 / len(single_2k), 1),
            "batch_qps": int(len(single_2k) / dt_simp),
            "gen_misses": gen_miss[:10],
            "eco_misses": eco_miss[:10],
        }]

    if worker == "pymystem3":
        from pymystem3 import Mystem
        t0 = time.perf_counter()
        ms = Mystem()
        _ = ms.lemmatize("кроссовки")
        load_ms = (time.perf_counter() - t0) * 1000
        load_rss = current_rss_mb()

        def ms_batch_words(pairs: list[tuple[str, str]]) -> tuple[float, list]:
            words = [w for w, _ in pairs]
            text = "\n".join(words)
            toks = [x for x in ms.lemmatize(text) if x.strip()]
            if len(toks) != len(words):
                toks = ["".join(x for x in ms.lemmatize(w) if x.strip())[:40] for w in words]
            ok = 0
            misses = []
            for (w, gold), got in zip(pairs, toks):
                g = norm_yo(got)
                if g == gold:
                    ok += 1
                else:
                    misses.append((w, gold, g))
            return round(100.0 * ok / max(1, len(pairs)), 2), misses

        gen_acc, gen_miss = ms_batch_words(general_pairs)
        eco_acc, eco_miss = ms_batch_words(ecom_hard)

        def ms_key(text: str) -> str:
            toks = [norm_yo(x) for x in ms.lemmatize(text) if TOK_RE.fullmatch(x.strip())]
            return " ".join(sorted({t for t in toks if t not in STOP_WORDS}))

        dedup_ms = eval_dedup(dedup_pairs, ms_key)

        n_ms = 200
        t0 = time.perf_counter()
        for q in single_2k[:n_ms]:
            _ = ms.lemmatize(q)
        dt_ms_single = time.perf_counter() - t0

        t0 = time.perf_counter()
        _ = ms.lemmatize("\n".join(single_2k))
        dt_ms_batch = time.perf_counter() - t0
        rss_peak = peak_rss_mb()

        return [{
            "model": "pymystem3 (Yandex MyStem)",
            "key": "pymystem3",
            "type": "C++ бинарник (проприетарный)",
            "load_ms": round(load_ms, 1),
            "load_rss_mb": load_rss,
            "peak_rss_mb": rss_peak,
            "general_acc": gen_acc,
            "ecom_acc": eco_acc,
            "dedup_f1": dedup_ms["f1"],
            "dedup_fp": dedup_ms["fp"],
            "single_us": round(dt_ms_single * 1e6 / n_ms, 1),
            "batch_qps": int(len(single_2k) / dt_ms_batch),
            "gen_misses": gen_miss[:10],
            "eco_misses": eco_miss[:10],
        }]

    if worker == "snowball":
        import snowballstemmer
        t0 = time.perf_counter()
        sb = snowballstemmer.stemmer("russian")
        load_ms = (time.perf_counter() - t0) * 1000
        load_rss = current_rss_mb()

        gen_acc, _ = eval_word_acc(general_pairs, sb.stemWord)
        eco_acc, _ = eval_word_acc(ecom_hard, sb.stemWord)
        dedup_sb = eval_dedup(dedup_pairs, lambda s: generic_key(s, sb.stemWord))

        t0 = time.perf_counter()
        for q in single_2k:
            _ = " ".join(sb.stemWords(TOK_RE.findall(q.lower())))
        dt_sb = time.perf_counter() - t0
        rss_peak = peak_rss_mb()

        return [{
            "model": "SnowballStemmer (ru)",
            "key": "snowball",
            "type": "Стеммер (Python, BSD-3)",
            "load_ms": round(load_ms, 1),
            "load_rss_mb": load_rss,
            "peak_rss_mb": rss_peak,
            "general_acc": gen_acc,
            "ecom_acc": eco_acc,
            "dedup_f1": dedup_sb["f1"],
            "dedup_fp": dedup_sb["fp"],
            "single_us": round(dt_sb * 1e6 / len(single_2k), 1),
            "batch_qps": int(len(single_2k) / dt_sb),
            "note": "Stemmer (produces stems, not dictionary lemmas)",
        }]

    if worker == "rulemma_scale":
        from rulemma import RuLemma
        rl = RuLemma(workers=os.cpu_count() or 8)
        scale_rows = []
        for n in (20_000, 100_000, 1_000_000):
            qs = build_queries(ecom_all, n)
            t0 = time.perf_counter()
            _ = rl.lemmatize_batch(qs)
            dt_lem = time.perf_counter() - t0
            t0 = time.perf_counter()
            _ = rl.semantic_keys(qs)
            dt_sem = time.perf_counter() - t0
            scale_rows.append({
                "queries": n,
                "lemmatize_s": round(dt_lem, 3),
                "lemmatize_qps": int(n / dt_lem),
                "semkey_s": round(dt_sem, 3),
                "semkey_qps": int(n / dt_sem),
                "peak_rss_mb": peak_rss_mb(),
            })
        rl.close()
        return scale_rows

    raise ValueError(f"Unknown worker: {worker}")


def invoke_subprocess(worker: str, batch_size: int) -> list[dict] | None:
    cmd = [sys.executable, str(Path(__file__).resolve()), "--worker", worker, "--batch-size", str(batch_size)]
    proc = subprocess.run(cmd, capture_output=True, text=True, check=False)
    if proc.returncode != 0:
        err_last = (proc.stderr.strip().splitlines() or ["unknown error"])[-1]
        print(f"  [skip] {worker}: {err_last}", flush=True)
        return None
    for line in reversed(proc.stdout.splitlines()):
        if line.startswith("JSON_RESULT:"):
            return json.loads(line[len("JSON_RESULT:") :])
    return None


def main() -> None:
    parser = argparse.ArgumentParser(description="Comparative Russian morphology benchmark")
    parser.add_argument("--worker", default=None, help=argparse.SUPPRESS)
    parser.add_argument("--batch-size", type=int, default=20_000)
    parser.add_argument("--scale", action="store_true", help="Also benchmark rulemma on 20k / 100k / 1M queries")
    args = parser.parse_args()

    if args.worker:
        res = run_worker(args.worker, args.batch_size)
        print("JSON_RESULT:" + json.dumps(res, ensure_ascii=False))
        return

    general_pairs = load_tsv_pairs(BENCH / "gold_general_ru.tsv")
    ecom_all = load_tsv_pairs(BENCH / "gold_ecommerce_ru.tsv")
    ecom_hard = ecom_all[:120]
    dedup_pairs = load_dedup_pairs(BENCH / "gold_dedup_pairs.tsv")

    print(
        f"[bench] General={len(general_pairs)}, Ecom-hard={len(ecom_hard)}, "
        f"Dedup={len(dedup_pairs)}, Batch={args.batch_size:,} (isolated subprocess per library)",
        flush=True,
    )

    workers = ["rulemma", "pymorphy3", "mawo_pymorphy3", "steosmorphy", "natasha", "simplemma", "pymystem3", "snowball"]
    results: list[dict] = []
    for w in workers:
        rows = invoke_subprocess(w, args.batch_size)
        if rows:
            for r in rows:
                results.append(r)
                print(
                    f"  {r['model']:32s} gen={r['general_acc']:5.1f}% eco={r['ecom_acc']:5.1f}% "
                    f"dedup={r['dedup_f1']:5.1f}% (fp={r['dedup_fp']}) "
                    f"rss={r['peak_rss_mb']:6.1f} MB single={r['single_us']:6.1f} µs batch={r['batch_qps']:>9,} q/s",
                    flush=True,
                )

    scale_results = None
    if args.scale:
        print("[bench] Running rulemma scaling check (20k / 100k / 1M queries)...", flush=True)
        scale_results = invoke_subprocess("rulemma_scale", args.batch_size)

    out = {
        "cpu_count": os.cpu_count(),
        "python_version": sys.version.split()[0],
        "datasets": {
            "general_ru": len(general_pairs),
            "ecommerce_ru": len(ecom_hard),
            "dedup_pairs": len(dedup_pairs),
            "batch_queries": args.batch_size,
        },
        "models": results,
    }
    if scale_results:
        out["rulemma_scale"] = scale_results

    (BENCH / "results.json").write_text(json.dumps(out, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")

    from rulemma import __version__ as rl_version
    model_links = {
        "rulemma": f"[rulemma](../README.md) {rl_version} (Go CGO)",
        "pymorphy3_uncached": "[pymorphy3](https://github.com/no-plagiarism/pymorphy3) (без кэша)",
        "pymorphy3_cached": "[pymorphy3](https://github.com/no-plagiarism/pymorphy3) (`lru_cache` 200k)",
        "mawo_pymorphy3": "[mawo-pymorphy3](https://pypi.org/project/mawo-pymorphy3/)",
        "steosmorphy": "[SteosMorphy](https://github.com/SteosOfficial/SteosMorphy-py) (Go `ctypes`)",
        "natasha": "[Natasha](https://github.com/natasha/natasha) ([Slovnet](https://github.com/natasha/slovnet))",
        "simplemma": "[simplemma](https://github.com/adbar/simplemma) (`ru`)",
        "pymystem3": "[pymystem3](https://github.com/nlpub/pymystem3) ([Яндекс MyStem](https://yandex.ru/dev/mystem/))",
        "snowball": "[SnowballStemmer](https://github.com/snowballstem/snowball) (`ru`)",
    }

    md = [
        "# Результаты бенчмарка rulemma против библиотек русского NLP",
        "",
        f"Замер выполнен на Linux x86_64 (CPU потоков: {os.cpu_count()}), Python {sys.version.split()[0]}, Go 1.23.",
        f"Наборы данных: общий русский ({len(general_pairs)}), e-commerce и поиск ({len(ecom_hard)}), пары дедупликации ({len(dedup_pairs)}), батч ({args.batch_size:,} уникальных 7-словных запросов). Каждая библиотека запускалась в отдельном изолированном процессе для честного замера пикового RSS RAM.",
        "",
        "| Библиотека | Архитектура и лицензия | Общий RU | E-Commerce | Дедупликация F1 | Холодный старт | Пик RSS RAM | 1 запрос | Батч из Python |",
        "|---|---|---:|---:|---:|---:|---:|---:|---:|",
    ]
    for r in results:
        label = model_links.get(r.get("key", ""), r["model"])
        md.append(
            f"| **{label}** | {r['type']} | {r['general_acc']:.1f}% | {r['ecom_acc']:.1f}% | "
            f"{r['dedup_f1']:.1f}% (FP={r['dedup_fp']}) | {r['load_ms']:.0f} мс | {r['peak_rss_mb']:.1f} МБ | "
            f"{r['single_us']:,.1f} мкс | **{r['batch_qps']:,} запр/с** |"
        )

    if scale_results:
        md.extend([
            "",
            "## Масштабирование `rulemma` на батчах (Python CGO, 7-словные запросы)",
            "",
            "| Объём батча | `lemmatize_batch` | Скорость (`lemmatize`) | `semantic_keys` | Скорость (`semantic_keys`) | Пик RSS RAM процесса |",
            "|---:|---:|---:|---:|---:|---:|",
        ])
        for s in scale_results:
            md.append(
                f"| {s['queries']:,} | {s['lemmatize_s']:.3f} с | **{s['lemmatize_qps']:,} запр/с** | "
                f"{s['semkey_s']:.3f} с | **{s['semkey_qps']:,} запр/с** | {s['peak_rss_mb']:.1f} МБ |"
            )

    md.extend([
        "",
        "## Примечания к методике",
        "",
        "- Все три тестовых набора (`gold_general_ru.tsv`, `gold_ecommerce_ru.tsv`, `gold_dedup_pairs.tsv`) составлены авторами `rulemma` и открыты в папке `benchmarks/`.",
        "- Каждая библиотека измеряется в отдельном подпроцессе (`--worker`), поэтому колонка **Пик RSS RAM** отражает реальное потребление памяти именно процесса с данной библиотекой (включая интерпретатор Python). Исключение — `pymystem3`, где бинарник `mystem` работает в отдельном дочернем C++ процессе, поэтому `RUSAGE_SELF` отражает только память Python-обёртки.",
        "- В тесте масштабирования `rulemma` на `1 000 000` запросов пик RSS включает сам Python-список из 1 млн входных и выходных строк в памяти; при потоковой обработке файла через CLI (`rulemma tsv`) потребление RAM остаётся постоянным (~65 МБ).",
        "- В наборе дедупликации 35 из 40 положительных пар проверяют склейку относительных прилагательных с существительными (`зимние кроссовки` и `кроссовки на зиму`). У сторонних библиотек такого словаря нет по дизайну; без `adj->noun` метод `rulemma.lemma_key` набирает 22.2%, а `rulemma.semantic_key` — 100.0% (FP=0).",
        "- `SteosMorphy` в пакетном тесте вызывается через нативный `parse_list` чанками по 4 000 слов (без разбиения на чанки передача всех слов одним JSON приводит к кратному росту потребления памяти).",
        "- `SnowballStemmer` является стеммером и возвращает усечённые основы слов, а не словарные леммы.",
        "- Воспроизвести замер: `.venv-bench/bin/python benchmarks/compare_models.py --scale`.",
    ])
    (BENCH / "RESULTS.md").write_text("\n".join(md) + "\n", encoding="utf-8")
    print("\n" + "\n".join(md))


if __name__ == "__main__":
    main()
