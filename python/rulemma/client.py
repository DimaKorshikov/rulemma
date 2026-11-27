"""High-level Python interface to rulemma (in-process Go C-shared library + CLI streaming fallback).

Single calls execute directly in-process via Go `librulemma.so` (zero subprocess spawn).
Batch calls (`lemmatize_batch`, `lemma_keys`, `semantic_keys`) execute in parallel Go goroutines,
bypassing Python's GIL for high-throughput query and catalog processing.
"""
from __future__ import annotations

import json
import math
import os
import re
import shutil
import subprocess
import tempfile
from pathlib import Path
from typing import Any, Iterable

from ._native import NativeLib, find_shared_library

# Neutral Russian prepositions & conjunctions only (no commercial terms, no negations like "без"/"не").
GENERAL_STOP_WORDS: frozenset[str] = frozenset({
    "в", "во", "на", "для", "и", "с", "со", "по",
    "из", "от", "до", "под", "к", "у", "о",
    "об", "а", "или",
})

# E-commerce stop-word profile: service prepositions + commercial intent noise.
# Negation markers ("без", "не", "нет") are intentionally excluded so opposite meanings
# ("шоколад без сахара" vs "шоколад с сахаром") never collide.
ECOMMERCE_STOP_WORDS: frozenset[str] = GENERAL_STOP_WORDS | frozenset({
    "купить", "цена", "цены", "недорого", "дешево", "интернет", "магазин",
})

DEFAULT_STOP_WORDS: frozenset[str] = ECOMMERCE_STOP_WORDS

_TOKEN_RE = re.compile(r"[a-zа-яё0-9]+(?:-[a-zа-яё0-9]+)*", re.IGNORECASE)
_WORD_SPAN_RE = re.compile(r"[a-zа-яё0-9]+", re.IGNORECASE)

_CYR_TO_LAT_HOMO = str.maketrans(
    "аАвВсСеЕнНкКмМоОрРтТхХуУ",
    "aAbBcCeEhHkKmMoOpPtTxXyY",
)
_LAT_TO_CYR_HOMO = str.maketrans(
    "aAcCeEkKmMoOpPtTxXyYhHbB",
    "аАсСеЕкКмМоОрРтТхХуУнНвВ",
)
_CYR_HOMO_SET = set("аАвВсСеЕнНкКмМоОрРтТхХуУ")
_LAT_HOMO_SET = set("aAcCeEkKmMoOpPtTxXyYhHbB")


def _is_cyr(ch: str) -> bool:
    return ("а" <= ch <= "я") or ("А" <= ch <= "Я") or ch in ("ё", "Ё")


def _is_lat(ch: str) -> bool:
    return ("a" <= ch <= "z") or ("A" <= ch <= "Z")


def _fix_token_homoglyphs(tok: str) -> str:
    has_cyr = has_lat = False
    uniq_cyr = uniq_lat = total_cyr = total_lat = 0
    for ch in tok:
        if _is_cyr(ch):
            has_cyr = True
            total_cyr += 1
            if ch not in _CYR_HOMO_SET:
                uniq_cyr += 1
        elif _is_lat(ch):
            has_lat = True
            total_lat += 1
            if ch not in _LAT_HOMO_SET:
                uniq_lat += 1
    if not (has_cyr and has_lat):
        return tok
    if uniq_lat > uniq_cyr:
        to_lat = True
    elif uniq_cyr > uniq_lat:
        to_lat = False
    else:
        to_lat = total_lat > total_cyr
    return tok.translate(_CYR_TO_LAT_HOMO if to_lat else _LAT_TO_CYR_HOMO)


def normalize_homoglyphs(text: str) -> str:
    """Repair mixed Cyrillic+Latin lookalike characters inside individual tokens.

    Pure Cyrillic and pure Latin tokens remain untouched.
    Examples: 'cм' -> 'см', 'рrо' -> 'pro', 'intеl' -> 'intel', 'аsus' -> 'asus', 'оzon' -> 'ozon'.
    """
    if not text:
        return ""
    return _WORD_SPAN_RE.sub(lambda m: _fix_token_homoglyphs(m.group(0)), text)


def pluralize_form(n: int | float, one: str, two: str, five: str) -> str:
    """Select the Russian noun form agreeing with numeric quantity n (e.g. 1/2/5/11/21/1.5)."""
    a = abs(float(n))
    if abs(a - math.trunc(a)) > 1e-9:
        return two
    iv = int(round(a))
    if 11 <= (iv % 100) <= 14:
        return five
    rem = iv % 10
    if rem == 1:
        return one
    if 2 <= rem <= 4:
        return two
    return five


_RUS_CONSONANTS = frozenset("бвгджзйклмнпрстфхцчшщ")


def _match_casing(orig: str, inflected: str) -> str:
    if not orig or not inflected:
        return inflected
    if len(orig) > 1 and orig.isupper():
        return inflected.upper()
    if orig[0].isupper():
        return inflected[0].upper() + inflected[1:]
    return inflected


def preposition(prep: str, next_word: str) -> str:
    """Select the natural Russian phonetic allomorph of a preposition (в/во, с/со, к/ко, о/об/обо)."""
    p = prep.strip()
    nw = next_word.strip().lower().replace("ё", "е")
    if not p or not nw:
        return p
    p_low = p.lower().replace("ё", "е")
    r0 = nw[0]
    r1 = nw[1] if len(nw) > 1 else ""
    chosen = p_low
    if p_low in ("в", "во"):
        chosen = "в"
        if (
            (r0 in ("в", "ф") and r1 in _RUS_CONSONANTS)
            or nw.startswith(("вс", "мног"))
            or nw in ("сне", "дворе", "льду", "рту", "время", "лбу", "мгле", "мрак", "мраке")
        ):
            chosen = "во"
    elif p_low in ("с", "со"):
        chosen = "с"
        if (
            (r0 in ("с", "з", "ш", "ж", "щ") and r1 in _RUS_CONSONANTS)
            or nw.startswith(("вс", "мн", "дн", "льн", "лб", "рт", "рж"))
            or nw in ("сна", "сном")
        ):
            chosen = "со"
    elif p_low in ("к", "ко"):
        chosen = "к"
        if (r0 == "к" and r1 in _RUS_CONSONANTS) or nw.startswith("вс") or nw in ("мне", "дню", "дну", "сну", "лбу", "рту"):
            chosen = "ко"
    elif p_low in ("о", "об", "обо"):
        chosen = "о"
        if nw in ("мне", "всем", "всех", "всей", "всему", "всего"):
            chosen = "обо"
        elif r0 in ("а", "и", "о", "у", "э"):
            chosen = "об"
    return _match_casing(p, chosen)


def _parse_semver(v: str) -> tuple[int, int, int]:
    m = re.fullmatch(r"(\d+)\.(\d+)\.(\d+)", v.strip())
    if not m:
        raise ValueError(f"Invalid semantic version: {v!r}")
    return int(m.group(1)), int(m.group(2)), int(m.group(3))


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
        min_version: str | None = None,
        strict_version: bool = False,
    ) -> None:
        self.data_dir = str(_find_data_dir(data_dir))
        self.overrides = str(overrides) if overrides else ""
        self.workers = workers or os.cpu_count() or 4

        self._native: NativeLib | None = None
        self._handle: int = 0
        self.binary: str | None = None
        self._manifest: dict[str, Any] | None = None

        manifest_path = Path(self.data_dir) / "manifest.json"
        if manifest_path.is_file():
            try:
                self._manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
            except Exception:
                self._manifest = None

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

        self._validate_versions(min_version=min_version, strict_version=strict_version)

    def _validate_versions(self, *, min_version: str | None, strict_version: bool) -> None:
        from . import __version__ as pkg_version

        active_version = self._native.version if self._native is not None else pkg_version
        if self._native is not None and self._manifest is not None:
            eng_min = str(self._manifest.get("engine_min_version", "")).strip()
            if eng_min and _parse_semver(active_version) < _parse_semver(eng_min):
                self.close()
                raise RuntimeError(
                    f"Loaded librulemma {active_version} is older than manifest engine_min_version "
                    f"{eng_min} ({self.data_dir}). Rebuild with: make build-lib"
                )
        if strict_version and self._native is not None and active_version != pkg_version:
            self.close()
            raise RuntimeError(
                f"Version mismatch: Python package rulemma=={pkg_version} != librulemma=={active_version}. "
                "Rebuild with: make build-lib"
            )
        if min_version is not None:
            if _parse_semver(active_version) < _parse_semver(min_version):
                self.close()
                raise RuntimeError(
                    f"rulemma version {active_version} is lower than required min_version={min_version}"
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

    @property
    def manifest(self) -> dict[str, Any] | None:
        """Return parsed data/manifest.json dictionary (or None if absent)."""
        return dict(self._manifest) if self._manifest is not None else None

    @property
    def dict_version(self) -> str:
        """Return dictionary version from data/manifest.json (e.g. '2026.11.3')."""
        if self._native is not None:
            dv = self._native.dict_version(self._handle)
            if dv:
                return dv
        if self._manifest is not None:
            return str(self._manifest.get("dict_version", ""))
        return ""

    def version_info(self) -> dict[str, Any]:
        """Return combined version metadata (package, native library, ABI, dictionary manifest)."""
        from . import __version__ as pkg_version

        return {
            "package_version": pkg_version,
            "library_version": self.library_version,
            "abi_version": self.abi_version,
            "dict_version": self.dict_version,
            "dict_schema_version": self._manifest.get("schema_version") if self._manifest else None,
            "backend": self.backend,
        }

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

    def normalize_homoglyphs(self, text: str) -> str:
        """Repair mixed Cyrillic+Latin homoglyphs inside individual tokens."""
        if self._native is not None and self._native._has_v03_tech:
            return self._native.normalize_homoglyphs(text)
        return normalize_homoglyphs(text)

    def tokenize_technical(
        self,
        text: str,
        *,
        normalize_homoglyphs: bool = True,
        split_hyphens: bool = False,
        split_number_unit: bool = True,
        compact_codes: bool = False,
    ) -> list[dict[str, Any]]:
        """Tokenize text with v0.3 technical e-commerce profile (decimals, dims, б/у, m.2, 18+, 12 В)."""
        if self._native is None or not self._native._has_v03_tech:
            raise RuntimeError("tokenize_technical() requires native backend v0.3+ (rebuild with: make build-lib)")
        opts = {
            "normalize_homoglyphs": bool(normalize_homoglyphs),
            "split_hyphens": bool(split_hyphens),
            "split_number_unit": bool(split_number_unit),
            "compact_codes": bool(compact_codes),
        }
        return self._native.tokenize_tech(text, opts)

    @staticmethod
    def _build_key_opts(
        *,
        semantic: bool,
        profile: str,
        stopwords: Iterable[str] | None,
        normalize_homoglyphs: bool,
        split_hyphens: bool,
        split_number_unit: bool,
        compact_codes: bool,
        preserve_prep_bindings: bool,
        bind_purpose_prep: bool,
        keep_words: Iterable[str] | None,
        exclude_adj_to_noun: Iterable[str] | None,
    ) -> dict[str, Any]:
        opts: dict[str, Any] = {
            "semantic": bool(semantic),
            "profile": profile or "default",
            "has_stopwords": stopwords is not None,
            "stopwords": [str(w).strip() for w in stopwords if str(w).strip()] if stopwords is not None else [],
            "normalize_homoglyphs": bool(normalize_homoglyphs),
            "split_hyphens": bool(split_hyphens),
            "split_number_unit": bool(split_number_unit),
            "compact_codes": bool(compact_codes),
            "preserve_prep_bindings": bool(preserve_prep_bindings),
            "bind_purpose_prep": bool(bind_purpose_prep),
            "keep_words": [str(w).strip() for w in keep_words if str(w).strip()] if keep_words else [],
            "exclude_adj_to_noun": (
                [str(w).strip() for w in exclude_adj_to_noun if str(w).strip()] if exclude_adj_to_noun else []
            ),
        }
        return opts

    def key(
        self,
        text: str,
        *,
        semantic: bool = True,
        profile: str = "technical",
        stopwords: Iterable[str] | None = None,
        normalize_homoglyphs: bool = True,
        split_hyphens: bool = False,
        split_number_unit: bool = True,
        compact_codes: bool = False,
        preserve_prep_bindings: bool = False,
        bind_purpose_prep: bool = False,
        keep_words: Iterable[str] | None = None,
        exclude_adj_to_noun: Iterable[str] | None = None,
    ) -> str:
        """Build a canonical search key with opt-in v0.3 technical tokenizer and preposition-binding options."""
        if self._native is not None and self._native._has_v03_tech:
            opts = self._build_key_opts(
                semantic=semantic,
                profile=profile,
                stopwords=stopwords,
                normalize_homoglyphs=normalize_homoglyphs,
                split_hyphens=split_hyphens,
                split_number_unit=split_number_unit,
                compact_codes=compact_codes,
                preserve_prep_bindings=preserve_prep_bindings,
                bind_purpose_prep=bind_purpose_prep,
                keep_words=keep_words,
                exclude_adj_to_noun=exclude_adj_to_noun,
            )
            return self._native.key_ex(self._handle, text, opts)
        return self.keys_batch(
            [text],
            semantic=semantic,
            profile=profile,
            stopwords=stopwords,
            normalize_homoglyphs=normalize_homoglyphs,
            split_hyphens=split_hyphens,
            split_number_unit=split_number_unit,
            compact_codes=compact_codes,
            preserve_prep_bindings=preserve_prep_bindings,
            bind_purpose_prep=bind_purpose_prep,
            keep_words=keep_words,
            exclude_adj_to_noun=exclude_adj_to_noun,
        )[0]

    def keys_batch(
        self,
        texts: Iterable[str],
        *,
        semantic: bool = True,
        profile: str = "technical",
        stopwords: Iterable[str] | None = None,
        normalize_homoglyphs: bool = True,
        split_hyphens: bool = False,
        split_number_unit: bool = True,
        compact_codes: bool = False,
        preserve_prep_bindings: bool = False,
        bind_purpose_prep: bool = False,
        keep_words: Iterable[str] | None = None,
        exclude_adj_to_noun: Iterable[str] | None = None,
    ) -> list[str]:
        """Build canonical keys for a batch of texts using parallel Go goroutines with v0.3 options."""
        items = list(texts)
        if not items:
            return []
        if self._native is not None and self._native._has_v03_tech:
            opts = self._build_key_opts(
                semantic=semantic,
                profile=profile,
                stopwords=stopwords,
                normalize_homoglyphs=normalize_homoglyphs,
                split_hyphens=split_hyphens,
                split_number_unit=split_number_unit,
                compact_codes=compact_codes,
                preserve_prep_bindings=preserve_prep_bindings,
                bind_purpose_prep=bind_purpose_prep,
                keep_words=keep_words,
                exclude_adj_to_noun=exclude_adj_to_noun,
            )
            return self._native.batch_ex(self._handle, items, opts, workers=self.workers)
        return self._cli_lines(
            items,
            mode="semkey" if semantic else "key",
            stopwords=stopwords,
            profile=profile,
            split_hyphens=split_hyphens,
            compact_codes=compact_codes,
            bind_preps=preserve_prep_bindings,
            bind_purpose=bind_purpose_prep,
            keep_words=keep_words,
            exclude_adj_to_noun=exclude_adj_to_noun,
        )

    def word(self, token: str) -> str:
        """Lemmatize a single token (ё -> е)."""
        if self._native is not None:
            return self._native.word(self._handle, token)
        return self.lemmatize_list([token])[0]

    def lookup_word(self, token: str) -> tuple[str, str]:
        """Lemmatize a single token and return (lemma, source).

        source is one of: 'override', 'dict', 'rule', 'oov'.
        """
        if self._native is not None:
            return self._native.lookup_word(self._handle, token)
        raise RuntimeError("lookup_word() requires native backend (librulemma.so)")

    def is_known(self, token: str) -> bool:
        """Return True when token is directly present in overrides or the base dictionary."""
        _, src = self.lookup_word(token)
        return src in ("override", "dict")

    def lemmatize(self, text: str) -> str:
        """Return space-joined lemmas for a single string, preserving token order."""
        if self._native is not None:
            return self._native.lemmatize(self._handle, text)
        return self.lemmatize_list([text])[0]

    def lemmatize_tokens(self, text: str) -> list[str]:
        """Return list of lemmas for a single string."""
        s = self.lemmatize(text)
        return s.split(" ") if s else []

    def lemma_key(
        self,
        text: str,
        *,
        stopwords: Iterable[str] | None = None,
        profile: str | None = None,
    ) -> str:
        """Return canonical sorted, deduplicated lemma key (excluding default or custom stop words)."""
        if profile and profile != "default":
            return self.key(text, semantic=False, profile=profile, stopwords=stopwords)
        if self._native is not None:
            if stopwords is None:
                return self._native.lemma_key(self._handle, text)
            return self._native.lemma_key_stop(self._handle, text, stopwords)
        return self.lemma_keys([text], stopwords=stopwords)[0]

    def semantic_key(
        self,
        text: str,
        *,
        stopwords: Iterable[str] | None = None,
        profile: str | None = None,
    ) -> str:
        """Return derivational-aware canonical key folding relational adjectives to nouns.

        Example: 'зимние кроссовки найк' == 'кроссовки на зиму найк' -> 'зима кроссовок найк'.
        """
        if profile and profile != "default":
            return self.key(text, semantic=True, profile=profile, stopwords=stopwords)
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

    # --- v0.3 Compact Morphology & Inflection API ---

    def analyze(self, word: str) -> dict[str, Any]:
        """Return compact morphological analysis (POS, gender, number, cases, flags, candidates) for a word."""
        if self._native is not None:
            return self._native.analyze(self._handle, word)
        raise RuntimeError("analyze() requires native backend (librulemma.so)")

    def word_candidates(self, word: str) -> list[dict[str, Any]]:
        """Return all valid dictionary (lemma, pos, cases) candidates for a word (e.g. 'пил' -> пить/пила)."""
        return list(self.analyze(word).get("candidates") or [])

    def prep_governs(self, prep: str, next_word: str) -> bool:
        """Report whether Russian preposition governs next_word (e.g. ('с', 'цинком') -> True, ('с', '1000') -> False)."""
        if self._native is not None:
            return self._native.prep_governs(self._handle, prep, next_word)
        raise RuntimeError("prep_governs() requires native backend (librulemma.so)")

    def inflect(self, word: str, case: str, number: str = "") -> tuple[str, bool]:
        """Inflect a single Russian noun or adjective/participle to case ('nomn'|'gent'|'datv'|'accs'|'ablt'|'loct')."""
        if self._native is not None:
            return self._native.inflect(self._handle, word, case, number)
        raise RuntimeError("inflect() requires native backend (librulemma.so)")

    def inflect_adj(
        self, adj: str, case: str, gender: str = "masc", number: str = "sing", *, animate: bool = False
    ) -> tuple[str, bool]:
        """Inflect a Russian adjective/participle to the specified case, gender, number, and animacy."""
        if self._native is not None:
            return self._native.inflect_adj(self._handle, adj, case, gender, number, animate=animate)
        raise RuntimeError("inflect_adj() requires native backend (librulemma.so)")

    def inflect_phrase(self, phrase: str, case: str, number: str = "") -> tuple[str, bool]:
        """Inflect a short Russian noun phrase preserving brands, SKU codes, and prepositional tails."""
        if self._native is not None:
            return self._native.inflect_phrase(self._handle, phrase, case, number)
        raise RuntimeError("inflect_phrase() requires native backend (librulemma.so)")

    def all_forms(self, word: str) -> list[str]:
        """Return all deduplicated inflected forms from the word's paradigm."""
        if self._native is not None:
            return self._native.all_forms(self._handle, word)
        raise RuntimeError("all_forms() requires native backend (librulemma.so)")

    def agree_number(self, n: int | float, word: str) -> str:
        """Format quantity + agreed Russian noun (e.g. agree_number(21, 'товар') -> '21 товар')."""
        if self._native is not None:
            return self._native.agree_number(self._handle, float(n), word)
        raise RuntimeError("agree_number() requires native backend (librulemma.so)")

    def preposition(self, prep: str, next_word: str) -> str:
        """Select the natural Russian phonetic allomorph of a preposition (в/во, с/со, к/ко, о/об/обо)."""
        if self._native is not None and self._native._has_v03_inflect:
            return self._native.preposition(prep, next_word)
        return preposition(prep, next_word)

    # --- High-throughput multi-core Go batch API (GIL-free!) ---

    def _cli_lines(
        self,
        texts: list[str],
        mode: str,
        *,
        stopwords: Iterable[str] | None = None,
        keep_yo: bool = False,
        profile: str | None = None,
        split_hyphens: bool = False,
        compact_codes: bool = False,
        bind_preps: bool = False,
        bind_purpose: bool = False,
        keep_words: Iterable[str] | None = None,
        exclude_adj_to_noun: Iterable[str] | None = None,
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
        if profile and profile != "default":
            cmd.extend(["--profile", profile])
        if split_hyphens:
            cmd.append("--split-hyphens")
        if compact_codes:
            cmd.append("--compact-codes")
        if bind_preps:
            cmd.append("--bind-preps")
        if bind_purpose:
            cmd.append("--bind-purpose")
        if keep_words:
            kw = ",".join(str(w).strip() for w in keep_words if str(w).strip())
            if kw:
                cmd.extend(["--keep-words", kw])
        if exclude_adj_to_noun:
            ex = ",".join(str(w).strip() for w in exclude_adj_to_noun if str(w).strip())
            if ex:
                cmd.extend(["--exclude-adj-to-noun", ex])

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

    def lemma_keys(
        self,
        texts: Iterable[str],
        *,
        stopwords: Iterable[str] | None = None,
        profile: str | None = None,
    ) -> list[str]:
        """Return canonical sorted, deduplicated lemma keys for a batch of texts in parallel Go."""
        if profile and profile != "default":
            return self.keys_batch(texts, semantic=False, profile=profile, stopwords=stopwords)
        items = list(texts)
        if not items:
            return []
        if self._native is not None:
            if stopwords is None:
                return self._native.batch(self._handle, items, mode=1, workers=self.workers)
            return self._native.batch_stop(self._handle, items, mode=1, stopwords=stopwords, workers=self.workers)
        return self._cli_lines(items, mode="key", stopwords=stopwords)

    def semantic_keys(
        self,
        texts: Iterable[str],
        *,
        stopwords: Iterable[str] | None = None,
        profile: str | None = None,
    ) -> list[str]:
        """Return derivational-aware semantic keys (adj->noun folded) for a batch of texts."""
        if profile and profile != "default":
            return self.keys_batch(texts, semantic=True, profile=profile, stopwords=stopwords)
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
        out: list[Any] = rows
        for i, r in enumerate(rows):
            if "\t" in r:
                a, b = r.split("\t", 1)
                out[i] = (a, b)
            else:
                out[i] = (r, "")
        return out  # type: ignore[return-value]

    def lemmatize_all(
        self,
        texts: Iterable[str],
        *,
        stopwords: Iterable[str] | None = None,
        profile: str | None = None,
        normalize_homoglyphs: bool = True,
        split_hyphens: bool = False,
        split_number_unit: bool = True,
        compact_codes: bool = False,
        preserve_prep_bindings: bool = False,
        bind_purpose_prep: bool = False,
        keep_words: Iterable[str] | None = None,
        exclude_adj_to_noun: Iterable[str] | None = None,
    ) -> list[tuple[str, str, str]]:
        """Return (lemmatized_text, lemma_key, semantic_key) triples in a single parallel Go pass.

        Without profile/key options the legacy v0.2 keys are returned unchanged. With profile="technical"
        or any key option (compact_codes, preserve_prep_bindings, keep_words, ...) both key columns are built
        with the same options as key()/keys_batch().
        """
        items = list(texts)
        n = len(items)
        if n == 0:
            return []
        use_ex = (bool(profile) and profile != "default") or bool(
            split_hyphens or compact_codes or preserve_prep_bindings or bind_purpose_prep or keep_words or exclude_adj_to_noun
        )
        if use_ex:
            opts = self._build_key_opts(
                semantic=False,
                profile=profile or "default",
                stopwords=stopwords,
                normalize_homoglyphs=normalize_homoglyphs if (profile and profile != "default") else False,
                split_hyphens=split_hyphens,
                split_number_unit=split_number_unit if (profile and profile != "default") else False,
                compact_codes=compact_codes,
                preserve_prep_bindings=preserve_prep_bindings,
                bind_purpose_prep=bind_purpose_prep,
                keep_words=keep_words,
                exclude_adj_to_noun=exclude_adj_to_noun,
            )

        def fetch(chunk: list[str]) -> list[str]:
            if use_ex:
                if self._native is not None and getattr(self._native, "_has_batch_all_ex", False):
                    return self._native.batch_all_ex(self._handle, chunk, opts, workers=self.workers)
                return self._cli_lines(
                    chunk,
                    mode="all",
                    stopwords=stopwords,
                    profile=profile,
                    split_hyphens=split_hyphens,
                    compact_codes=compact_codes,
                    bind_preps=preserve_prep_bindings,
                    bind_purpose=bind_purpose_prep,
                    keep_words=keep_words,
                    exclude_adj_to_noun=exclude_adj_to_noun,
                )
            if self._native is not None:
                return (
                    self._native.batch(self._handle, chunk, mode=4, workers=self.workers)
                    if stopwords is None
                    else self._native.batch_stop(self._handle, chunk, mode=4, stopwords=stopwords, workers=self.workers)
                )
            return self._cli_lines(chunk, mode="all", stopwords=stopwords)

        def to_triple(r: str) -> tuple[str, str, str]:
            parts = r.split("\t")
            if len(parts) >= 3:
                return (parts[0], parts[1], parts[2])
            if len(parts) == 2:
                return (parts[0], parts[1], "")
            return (r, "", "")

        # Process large batches in 50k chunks + in-place tuple conversion to keep peak RAM lower than 3 separate calls.
        chunk_size = 50_000
        if n <= chunk_size:
            out: list[Any] = fetch(items)
            for i, r in enumerate(out):
                out[i] = to_triple(r)
            return out  # type: ignore[return-value]

        result: list[tuple[str, str, str]] = []
        for start in range(0, n, chunk_size):
            rows = fetch(items[start : start + chunk_size])
            result.extend(to_triple(r) for r in rows)
            del rows
        return result

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
        profile: str | None = None,
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
        if profile and profile != "default":
            cmd.extend(["--profile", profile])
        if header:
            cmd.append("--header")
        proc = subprocess.run(cmd, capture_output=True, text=True, check=True)
        return proc.stderr.strip()
