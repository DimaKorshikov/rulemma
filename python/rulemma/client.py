"""High-level Python interface to rulemma (in-process Go C-shared library + CLI streaming fallback).

Single calls execute directly in-process via Go `librulemma.so` (zero subprocess spawn).
Batch calls (`lemmatize_batch`, `lemma_keys`, `semantic_keys`) execute in parallel Go goroutines,
bypassing Python's GIL for high-throughput query and catalog processing.
"""
from __future__ import annotations

import os
import re
import shutil
import subprocess
import tempfile
from pathlib import Path
from typing import Iterable

from ._native import NativeLib, find_shared_library

DEFAULT_STOP_WORDS: frozenset[str] = frozenset({
    "в", "во", "на", "для", "и", "с", "со", "по",
    "из", "от", "до", "без", "под", "к", "у", "о",
    "об", "а", "или", "купить", "цена", "цены",
    "недорого", "дешево", "интернет", "магазин",
})

_TOKEN_RE = re.compile(r"[a-zа-яё0-9]+(?:-[a-zа-яё0-9]+)*", re.IGNORECASE)


def normalize_phrase(text: str, *, keep_yo: bool = False) -> str:
    """Normalize a Russian/e-commerce phrase identically to Go `rulemma.NormalizePhrase`.

    Lowercases, strips punctuation and repeated whitespace while preserving Cyrillic,
    Latin, digits, and internal hyphens. Normalizes 'ё' -> 'е' unless keep_yo=True.
    """
    toks = _TOKEN_RE.findall(text.lower())
    if not toks:
        return ""
    res = " ".join(toks)
    return res if keep_yo else res.replace("ё", "е")


def _find_data_dir(explicit: str | Path | None = None) -> Path:
    if explicit is not None:
        return Path(explicit)
    if env := os.environ.get("RULEMMA_DATA"):
        return Path(env)
    pkg_dir = Path(__file__).resolve().parent
    for cand in (pkg_dir / "data", pkg_dir.parents[1] / "data", Path("data")):
        if (cand / "lemmas.tsv.gz").exists() or (cand / "lemmas.tsv").exists():
            return cand
    return pkg_dir.parents[1] / "data"


class RuLemma:
    """Fast, concurrent Russian & e-commerce lemmatizer backed by Go."""

    def __init__(
        self,
        *,
        data_dir: str | Path | None = None,
        overrides: str | Path | None = None,
        workers: int | None = None,
        lib_path: str | Path | None = None,
        binary: str | Path | None = None,
        prefer_native: bool = True,
    ) -> None:
        self.data_dir = str(_find_data_dir(data_dir))
        self.overrides = str(overrides) if overrides else ""
        self.workers = workers or os.cpu_count() or 4

        self._native: NativeLib | None = None
        self._handle: int = 0
        self.binary: str | None = None

        if prefer_native:
            so_path = find_shared_library(lib_path)
            if so_path is not None:
                self._native = NativeLib(so_path)
                self._handle = self._native.load(self.data_dir, self.overrides)

        repo_root = Path(__file__).resolve().parents[2]
        if binary is not None:
            self.binary = str(binary)
        elif (repo_root / "bin" / "rulemma").exists():
            self.binary = str(repo_root / "bin" / "rulemma")
        else:
            self.binary = shutil.which("rulemma")

        if self._native is None and self.binary is None:
            raise FileNotFoundError(
                "Neither librulemma.so nor rulemma CLI binary found. "
                "Build them with: make build (requires Go 1.21+)."
            )

    @property
    def backend(self) -> str:
        """Return 'native' (in-process CGO shared lib) or 'cli' (subprocess fallback)."""
        return "native" if self._native is not None else "cli"

    @property
    def abi_version(self) -> int:
        """Return C-ABI version of the loaded native library (0 when using CLI backend)."""
        return self._native.abi_version if self._native is not None else 0

    @property
    def library_version(self) -> str:
        """Return semantic version reported by the loaded native library."""
        return self._native.version if self._native is not None else "cli"

    def close(self) -> None:
        if self._native is not None and self._handle:
            self._native.free(self._handle)
            self._handle = 0

    def __enter__(self) -> "RuLemma":
        return self

    def __exit__(self, *args: object) -> None:
        self.close()

    def __del__(self) -> None:
        try:
            self.close()
        except Exception:
            pass

    def stats(self) -> dict[str, int]:
        if self._native is not None:
            return self._native.stats(self._handle)
        raise RuntimeError("stats() requires native backend (librulemma.so)")

    # --- Single-item microsecond API ---

    def normalize_phrase(self, text: str, *, keep_yo: bool = False) -> str:
        """Normalize a phrase (lowercase, strip punctuation, optionally preserve 'ё')."""
        if self._native is not None and self._native._has_normalize:
            return self._native.normalize_phrase(text, keep_yo=keep_yo)
        return normalize_phrase(text, keep_yo=keep_yo)

    def word(self, token: str) -> str:
        """Lemmatize a single token (ё -> е)."""
        if self._native is not None:
            return self._native.word(self._handle, token)
        return self.lemmatize_list([token])[0]

    def lemmatize(self, text: str) -> str:
        """Return space-joined lemmas for a single string, preserving token order."""
        if self._native is not None:
            return self._native.lemmatize(self._handle, text)
        return self.lemmatize_list([text])[0]

    def lemmatize_tokens(self, text: str) -> list[str]:
        """Return list of lemmas for a single string."""
        s = self.lemmatize(text)
        return s.split(" ") if s else []

    def lemma_key(self, text: str, *, stopwords: Iterable[str] | None = None) -> str:
        """Return canonical sorted, deduplicated lemma key (excluding default or custom stop words)."""
        if self._native is not None:
            if stopwords is None:
                return self._native.lemma_key(self._handle, text)
            return self._native.lemma_key_stop(self._handle, text, stopwords)
        return self.lemma_keys([text], stopwords=stopwords)[0]

    def semantic_key(self, text: str, *, stopwords: Iterable[str] | None = None) -> str:
        """Return derivational-aware canonical key folding relational adjectives to nouns.

        Example: 'зимние кроссовки найк' == 'кроссовки на зиму найк' -> 'зима кроссовок найк'.
        """
        if self._native is not None:
            if stopwords is None:
                return self._native.semantic_key(self._handle, text)
            return self._native.semantic_key_stop(self._handle, text, stopwords)
        return self.semantic_keys([text], stopwords=stopwords)[0]

    def adj_to_noun(self, word: str) -> tuple[str, bool]:
        """Map a relational adjective (any form) to its canonical noun, e.g. 'зимние' -> ('зима', True)."""
        if self._native is not None:
            return self._native.adj_to_noun(self._handle, word)
        raise RuntimeError("adj_to_noun() requires native backend (librulemma.so)")

    def agree_adjective(self, adj: str, product_type: str) -> tuple[str, bool]:
        """Inflect a Russian adjective to agree in gender/number with product_type."""
        if self._native is not None:
            return self._native.agree_adjective(self._handle, adj, product_type)
        raise RuntimeError("agree_adjective() requires native backend (librulemma.so)")

    def build_seo_name(
        self, base_name: str, product_type: str, keywords: Iterable[str], max_len: int = 100
    ) -> tuple[str, int, int]:
        """Build a deduplicated, grammatically agreed Russian SEO title up to max_len characters."""
        if self._native is not None:
            return self._native.build_seo_name(self._handle, base_name, product_type, list(keywords), max_len)
        raise RuntimeError("build_seo_name() requires native backend (librulemma.so)")

    # --- High-throughput multi-core Go batch API (GIL-free!) ---

    def _cli_lines(
        self,
        texts: list[str],
        mode: str,
        *,
        stopwords: Iterable[str] | None = None,
        keep_yo: bool = False,
    ) -> list[str]:
        if not self.binary:
            raise FileNotFoundError("rulemma CLI binary not found")
        if not texts:
            return []
        clean = [str(t).replace("\n", " ").replace("\r", " ") for t in texts]
        payload = ("\n".join(clean) + "\n").encode("utf-8")
        cmd = [self.binary, "lemmatize", "--data", self.data_dir, "--workers", str(self.workers), "--mode", mode]
        if self.overrides:
            cmd.extend(["--overrides", self.overrides])
        if keep_yo:
            cmd.append("--keep-yo")

        tmp_stop: Path | None = None
        try:
            if stopwords is not None:
                sw_list = [str(w).strip() for w in stopwords if str(w).strip()]
                if not sw_list:
                    cmd.extend(["--stopwords", "none"])
                else:
                    with tempfile.NamedTemporaryFile("w", encoding="utf-8", suffix=".txt", delete=False) as tf:
                        tf.write("\n".join(sw_list) + "\n")
                        tmp_stop = Path(tf.name)
                    cmd.extend(["--stopwords", str(tmp_stop)])
            proc = subprocess.run(cmd, input=payload, capture_output=True, check=True)
        finally:
            if tmp_stop is not None:
                try:
                    tmp_stop.unlink()
                except OSError:
                    pass

        out = proc.stdout.decode("utf-8").splitlines()
        if len(out) != len(clean):
            raise RuntimeError(f"rulemma returned {len(out)} lines for {len(clean)} inputs")
        return out

    def normalize_batch(self, texts: Iterable[str], *, keep_yo: bool = False) -> list[str]:
        """Normalize a batch of phrases across parallel Go goroutines."""
        items = list(texts)
        if not items:
            return []
        if self._native is not None and self._native._has_normalize:
            return self._native.batch(self._handle, items, mode=6 if keep_yo else 5, workers=self.workers)
        return [normalize_phrase(s, keep_yo=keep_yo) for s in items]

    def lemmatize_batch(self, texts: Iterable[str]) -> list[str]:
        """Lemmatize a batch of texts across parallel Go goroutines (bypasses Python GIL)."""
        items = list(texts)
        if not items:
            return []
        if self._native is not None:
            return self._native.batch(self._handle, items, mode=0, workers=self.workers)
        return self._cli_lines(items, mode="text")

    # Backward-compatible alias
    lemmatize_list = lemmatize_batch

    def lemma_keys(self, texts: Iterable[str], *, stopwords: Iterable[str] | None = None) -> list[str]:
        """Return canonical sorted, deduplicated lemma keys for a batch of texts in parallel Go."""
        items = list(texts)
        if not items:
            return []
        if self._native is not None:
            if stopwords is None:
                return self._native.batch(self._handle, items, mode=1, workers=self.workers)
            return self._native.batch_stop(self._handle, items, mode=1, stopwords=stopwords, workers=self.workers)
        return self._cli_lines(items, mode="key", stopwords=stopwords)

    def semantic_keys(self, texts: Iterable[str], *, stopwords: Iterable[str] | None = None) -> list[str]:
        """Return derivational-aware semantic keys (adj->noun folded) for a batch of texts."""
        items = list(texts)
        if not items:
            return []
        if self._native is not None:
            if stopwords is None:
                return self._native.batch(self._handle, items, mode=2, workers=self.workers)
            return self._native.batch_stop(self._handle, items, mode=2, stopwords=stopwords, workers=self.workers)
        return self._cli_lines(items, mode="semkey", stopwords=stopwords)

    def lemmatize_and_keys(
        self, texts: Iterable[str], *, stopwords: Iterable[str] | None = None
    ) -> list[tuple[str, str]]:
        """Return (lemmatized_text, lemma_key) pairs in a single parallel Go pass."""
        items = list(texts)
        if not items:
            return []
        if self._native is not None:
            rows = (
                self._native.batch(self._handle, items, mode=3, workers=self.workers)
                if stopwords is None
                else self._native.batch_stop(self._handle, items, mode=3, stopwords=stopwords, workers=self.workers)
            )
        else:
            rows = self._cli_lines(items, mode="both", stopwords=stopwords)
        return [tuple(r.split("\t", 1)) if "\t" in r else (r, "") for r in rows]  # type: ignore[misc]

    def lemmatize_tsv(
        self,
        in_path: str | Path,
        out_path: str | Path,
        *,
        col: int = 0,
        mode: str = "both",
        header: bool = True,
        stopwords: str | Path | None = None,
        keep_yo: bool = False,
    ) -> str:
        """Stream a large TSV file directly through Go CLI workers (millions of rows/sec)."""
        if not self.binary:
            raise FileNotFoundError("lemmatize_tsv requires the rulemma CLI binary (make build-cli)")
        cmd = [
            self.binary, "tsv",
            "--data", self.data_dir,
            "--workers", str(self.workers),
            "--in", str(in_path),
            "--out", str(out_path),
            "--col", str(col),
            "--mode", mode,
        ]
        if self.overrides:
            cmd.extend(["--overrides", self.overrides])
        if stopwords is not None:
            cmd.extend(["--stopwords", str(stopwords)])
        if keep_yo:
            cmd.append("--keep-yo")
        if header:
            cmd.append("--header")
        proc = subprocess.run(cmd, capture_output=True, text=True, check=True)
        return proc.stderr.strip()
