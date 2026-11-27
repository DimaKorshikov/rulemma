"""Low-level ctypes binding to the Go C-shared library (`librulemma.so` / `.dylib` / `.dll`)."""
from __future__ import annotations

import ctypes
import platform
from pathlib import Path
from typing import Iterable

EXPECTED_ABI_VERSION = 1


def _lib_filename() -> str:
    s = platform.system()
    if s == "Darwin":
        return "librulemma.dylib"
    if s == "Windows":
        return "rulemma.dll"
    return "librulemma.so"


def find_shared_library(explicit: str | Path | None = None) -> Path | None:
    if explicit is not None:
        p = Path(explicit)
        return p if p.exists() else None
    fname = _lib_filename()
    pkg_dir = Path(__file__).resolve().parent
    repo_root = pkg_dir.parents[1]
    for cand in (pkg_dir / fname, repo_root / "bin" / fname, repo_root / fname):
        if cand.exists():
            return cand
    return None


def _encode_stopwords(stopwords: Iterable[str] | None) -> bytes | None:
    if stopwords is None:
        return None
    words = [str(w).strip() for w in stopwords if str(w).strip()]
    return "\n".join(words).encode("utf-8")


class NativeLib:
    """Thin ctypes facade with safe UTF-8 conversion and automatic C-string freeing."""

    def __init__(self, lib_path: Path) -> None:
        self.path = lib_path
        lib = ctypes.CDLL(str(lib_path))
        self._lib = lib
        # Use libc free() directly instead of crossing the Go CGO boundary a second time just to free C.CString!
        self._libc_free = None
        try:
            libc = ctypes.CDLL(None)
            libc.free.argtypes = [ctypes.c_void_p]
            libc.free.restype = None
            self._libc_free = libc.free
        except Exception:
            self._libc_free = None

        # Always declare restype = c_void_p for heap-allocated *char so ctypes does NOT lose the pointer before free!
        lib.rulemma_last_error.argtypes = []
        lib.rulemma_last_error.restype = ctypes.c_void_p

        lib.rulemma_free_str.argtypes = [ctypes.c_void_p]
        lib.rulemma_free_str.restype = None

        # Check C-ABI version (added in v0.2.1; tolerate pre-0.2.1 libs gracefully for backward compatibility)
        self.abi_version: int = 0
        self.version: str = "0.2.0"
        if hasattr(lib, "rulemma_abi_version"):
            lib.rulemma_abi_version.argtypes = []
            lib.rulemma_abi_version.restype = ctypes.c_int
            self.abi_version = int(lib.rulemma_abi_version())
            if self.abi_version != EXPECTED_ABI_VERSION:
                raise RuntimeError(
                    f"Incompatible librulemma ABI version {self.abi_version} "
                    f"(expected {EXPECTED_ABI_VERSION}). Rebuild with: make build-lib"
                )
        if hasattr(lib, "rulemma_version"):
            lib.rulemma_version.argtypes = []
            lib.rulemma_version.restype = ctypes.c_void_p
            self.version = self._take_str(lib.rulemma_version())

        lib.rulemma_load.argtypes = [ctypes.c_char_p, ctypes.c_char_p]
        lib.rulemma_load.restype = ctypes.c_int64

        lib.rulemma_free.argtypes = [ctypes.c_int64]
        lib.rulemma_free.restype = None

        lib.rulemma_stats.argtypes = [
            ctypes.c_int64,
            ctypes.POINTER(ctypes.c_int64),
            ctypes.POINTER(ctypes.c_int64),
            ctypes.POINTER(ctypes.c_int64),
            ctypes.POINTER(ctypes.c_int64),
            ctypes.POINTER(ctypes.c_int64),
        ]
        lib.rulemma_stats.restype = ctypes.c_int

        for fn_name in ("rulemma_word", "rulemma_lemmatize", "rulemma_lemma_key", "rulemma_semantic_key"):
            fn = getattr(lib, fn_name)
            fn.argtypes = [ctypes.c_int64, ctypes.c_char_p]
            fn.restype = ctypes.c_void_p

        self._has_normalize = hasattr(lib, "rulemma_normalize_phrase")
        if self._has_normalize:
            lib.rulemma_normalize_phrase.argtypes = [ctypes.c_char_p, ctypes.c_int]
            lib.rulemma_normalize_phrase.restype = ctypes.c_void_p

        self._has_stop_api = (
            hasattr(lib, "rulemma_lemma_key_stop")
            and hasattr(lib, "rulemma_semantic_key_stop")
            and hasattr(lib, "rulemma_batch_stop")
        )
        if self._has_stop_api:
            lib.rulemma_lemma_key_stop.argtypes = [ctypes.c_int64, ctypes.c_char_p, ctypes.c_char_p]
            lib.rulemma_lemma_key_stop.restype = ctypes.c_void_p

            lib.rulemma_semantic_key_stop.argtypes = [ctypes.c_int64, ctypes.c_char_p, ctypes.c_char_p]
            lib.rulemma_semantic_key_stop.restype = ctypes.c_void_p

            lib.rulemma_batch_stop.argtypes = [
                ctypes.c_int64,
                ctypes.c_char_p,
                ctypes.c_int,
                ctypes.c_int,
                ctypes.c_char_p,
                ctypes.c_int,
                ctypes.POINTER(ctypes.c_int),
            ]
            lib.rulemma_batch_stop.restype = ctypes.c_void_p

        lib.rulemma_adj_to_noun.argtypes = [ctypes.c_int64, ctypes.c_char_p, ctypes.POINTER(ctypes.c_int)]
        lib.rulemma_adj_to_noun.restype = ctypes.c_void_p

        lib.rulemma_agree_adjective.argtypes = [
            ctypes.c_int64, ctypes.c_char_p, ctypes.c_char_p, ctypes.POINTER(ctypes.c_int)
        ]
        lib.rulemma_agree_adjective.restype = ctypes.c_void_p

        lib.rulemma_build_seo_name.argtypes = [
            ctypes.c_int64,
            ctypes.c_char_p,
            ctypes.c_char_p,
            ctypes.c_char_p,
            ctypes.c_int,
            ctypes.POINTER(ctypes.c_int),
            ctypes.POINTER(ctypes.c_int),
        ]
        lib.rulemma_build_seo_name.restype = ctypes.c_void_p

        lib.rulemma_batch.argtypes = [
            ctypes.c_int64,
            ctypes.c_char_p,
            ctypes.c_int,
            ctypes.c_int,
            ctypes.c_int,
            ctypes.POINTER(ctypes.c_int),
        ]
        lib.rulemma_batch.restype = ctypes.c_void_p

    def _free_ptr(self, ptr: int) -> None:
        if self._libc_free is not None:
            self._libc_free(ptr)
        else:
            self._lib.rulemma_free_str(ptr)

    def _take_str(self, ptr: int | None) -> str:
        if not ptr:
            return ""
        try:
            raw = ctypes.string_at(ptr)
            return raw.decode("utf-8")
        finally:
            self._free_ptr(ptr)

    def _take_bytes_n(self, ptr: int | None, n: int) -> str:
        if not ptr:
            return ""
        try:
            raw = ctypes.string_at(ptr, n)
            return raw.decode("utf-8")
        finally:
            self._free_ptr(ptr)

    def load(self, data_dir: str, overrides: str = "") -> int:
        ov_b = overrides.encode("utf-8") if overrides else None
        h = int(self._lib.rulemma_load(data_dir.encode("utf-8"), ov_b))
        if h == 0:
            err = self._take_str(self._lib.rulemma_last_error())
            raise RuntimeError(f"rulemma_load failed ({data_dir}): {err}")
        return h

    def free(self, handle: int) -> None:
        if handle:
            self._lib.rulemma_free(handle)

    def stats(self, handle: int) -> dict[str, int]:
        a, b, c, d, e = (ctypes.c_int64() for _ in range(5))
        ok = self._lib.rulemma_stats(handle, ctypes.byref(a), ctypes.byref(b), ctypes.byref(c), ctypes.byref(d), ctypes.byref(e))
        if not ok:
            raise RuntimeError("invalid rulemma handle")
        return {
            "lemmas": int(a.value),
            "types": int(b.value),
            "adj_forms": int(c.value),
            "adj_noun": int(d.value),
            "overrides": int(e.value),
        }

    def normalize_phrase(self, text: str, *, keep_yo: bool = False) -> str:
        if not self._has_normalize:
            raise RuntimeError("librulemma does not export rulemma_normalize_phrase; rebuild with: make build-lib")
        return self._take_str(self._lib.rulemma_normalize_phrase(text.encode("utf-8"), 1 if keep_yo else 0))

    def word(self, handle: int, w: str) -> str:
        return self._take_str(self._lib.rulemma_word(handle, w.encode("utf-8")))

    def lemmatize(self, handle: int, text: str) -> str:
        return self._take_str(self._lib.rulemma_lemmatize(handle, text.encode("utf-8")))

    def lemma_key(self, handle: int, text: str) -> str:
        return self._take_str(self._lib.rulemma_lemma_key(handle, text.encode("utf-8")))

    def lemma_key_stop(self, handle: int, text: str, stopwords: Iterable[str] | None) -> str:
        if stopwords is None:
            return self.lemma_key(handle, text)
        if not self._has_stop_api:
            raise RuntimeError("librulemma does not export rulemma_lemma_key_stop; rebuild with: make build-lib")
        stop_b = _encode_stopwords(stopwords)
        return self._take_str(self._lib.rulemma_lemma_key_stop(handle, text.encode("utf-8"), stop_b))

    def semantic_key(self, handle: int, text: str) -> str:
        return self._take_str(self._lib.rulemma_semantic_key(handle, text.encode("utf-8")))

    def semantic_key_stop(self, handle: int, text: str, stopwords: Iterable[str] | None) -> str:
        if stopwords is None:
            return self.semantic_key(handle, text)
        if not self._has_stop_api:
            raise RuntimeError("librulemma does not export rulemma_semantic_key_stop; rebuild with: make build-lib")
        stop_b = _encode_stopwords(stopwords)
        return self._take_str(self._lib.rulemma_semantic_key_stop(handle, text.encode("utf-8"), stop_b))

    def adj_to_noun(self, handle: int, w: str) -> tuple[str, bool]:
        matched = ctypes.c_int(0)
        res = self._take_str(self._lib.rulemma_adj_to_noun(handle, w.encode("utf-8"), ctypes.byref(matched)))
        return res, bool(matched.value)

    def agree_adjective(self, handle: int, adj: str, product_type: str) -> tuple[str, bool]:
        changed = ctypes.c_int(0)
        res = self._take_str(
            self._lib.rulemma_agree_adjective(handle, adj.encode("utf-8"), product_type.encode("utf-8"), ctypes.byref(changed))
        )
        return res, bool(changed.value)

    def build_seo_name(self, handle: int, base_name: str, product_type: str, keywords: list[str], max_len: int = 100) -> tuple[str, int, int]:
        added = ctypes.c_int(0)
        agreed = ctypes.c_int(0)
        kw_s = "\n".join(keywords).encode("utf-8")
        res = self._take_str(
            self._lib.rulemma_build_seo_name(
                handle, base_name.encode("utf-8"), product_type.encode("utf-8"), kw_s, int(max_len),
                ctypes.byref(added), ctypes.byref(agreed),
            )
        )
        return res, int(added.value), int(agreed.value)

    def batch(self, handle: int, texts: list[str], mode: int, workers: int) -> list[str]:
        if not texts:
            return []
        payload = "\n".join(str(t).replace("\n", " ").replace("\r", " ") for t in texts).encode("utf-8")
        out_len = ctypes.c_int(0)
        ptr = self._lib.rulemma_batch(handle, payload, len(payload), int(mode), int(workers), ctypes.byref(out_len))
        joined = self._take_bytes_n(ptr, int(out_len.value))
        return joined.split("\n")

    def batch_stop(
        self,
        handle: int,
        texts: list[str],
        mode: int,
        stopwords: Iterable[str] | None,
        workers: int,
    ) -> list[str]:
        if stopwords is None:
            return self.batch(handle, texts, mode, workers)
        if not texts:
            return []
        if not self._has_stop_api:
            raise RuntimeError("librulemma does not export rulemma_batch_stop; rebuild with: make build-lib")
        payload = "\n".join(str(t).replace("\n", " ").replace("\r", " ") for t in texts).encode("utf-8")
        stop_b = _encode_stopwords(stopwords)
        out_len = ctypes.c_int(0)
        ptr = self._lib.rulemma_batch_stop(
            handle, payload, len(payload), int(mode), stop_b, int(workers), ctypes.byref(out_len)
        )
        joined = self._take_bytes_n(ptr, int(out_len.value))
        return joined.split("\n")
