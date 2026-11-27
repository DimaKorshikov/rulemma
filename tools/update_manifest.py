"""Generate or verify data/manifest.json (schema_version=1) for rulemma dictionaries."""
from __future__ import annotations

import argparse
import gzip
import hashlib
import json
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
DATA_DIR = ROOT / "data"

SCHEMA_VERSION = 1
DEFAULT_DICT_VERSION = "2026.11.5"
DEFAULT_ENGINE_MIN_VERSION = "0.3.0"

DICT_FILES = {
    "lemmas": "lemmas.tsv.gz",
    "adj_forms": "adj_forms.tsv.gz",
    "types": "types.tsv.gz",
    "adj_noun": "adj_noun.tsv.gz",
    "morph": "morph.tsv.gz",
    "alt_lemmas": "alt_lemmas.tsv.gz",
    "paradigms": "paradigms.tsv.gz",
    "overrides": "overrides.tsv",
}


def sha256_file(path: Path) -> str:
    h = hashlib.sha256()
    with path.open("rb") as f:
        for chunk in iter(lambda: f.read(65536), b""):
            h.update(chunk)
    return h.hexdigest()


def count_entries(path: Path) -> int:
    opener = gzip.open if path.suffix == ".gz" else open
    cnt = 0
    with opener(path, "rt", encoding="utf-8") as f:
        for line in f:
            s = line.rstrip("\r\n")
            if not s.strip() or s.lstrip().startswith("#"):
                continue
            if "\t" in s:
                cnt += 1
    return cnt


def build_manifest(
    data_dir: Path,
    *,
    dict_version: str = DEFAULT_DICT_VERSION,
    engine_min_version: str = DEFAULT_ENGINE_MIN_VERSION,
) -> dict:
    files: dict[str, str] = {}
    counts: dict[str, int] = {}
    sha256: dict[str, str] = {}
    for key, fname in DICT_FILES.items():
        p = data_dir / fname
        if not p.exists():
            raise FileNotFoundError(f"Missing dictionary file: {p}")
        files[key] = fname
        counts[key] = count_entries(p)
        sha256[fname] = sha256_file(p)
    return {
        "schema_version": SCHEMA_VERSION,
        "dict_version": dict_version,
        "engine_min_version": engine_min_version,
        "files": files,
        "counts": counts,
        "sha256": sha256,
    }


def main() -> int:
    parser = argparse.ArgumentParser(description="Generate or verify rulemma data/manifest.json")
    parser.add_argument("--data-dir", type=Path, default=DATA_DIR)
    parser.add_argument("--dict-version", default=DEFAULT_DICT_VERSION)
    parser.add_argument("--engine-min-version", default=DEFAULT_ENGINE_MIN_VERSION)
    parser.add_argument("--check", action="store_true", help="Verify existing manifest.json matches files on disk")
    args = parser.parse_args()

    manifest = build_manifest(
        args.data_dir,
        dict_version=args.dict_version,
        engine_min_version=args.engine_min_version,
    )
    out_path = args.data_dir / "manifest.json"

    if args.check:
        if not out_path.exists():
            print(f"[manifest] FAIL: {out_path} does not exist", file=sys.stderr)
            return 1
        existing = json.loads(out_path.read_text(encoding="utf-8"))
        for field in ("schema_version", "files", "counts", "sha256"):
            if existing.get(field) != manifest.get(field):
                print(f"[manifest] FAIL: mismatch in {field}", file=sys.stderr)
                return 1
        print(f"[manifest] OK: {out_path} verified ({manifest['counts']})")
        return 0

    out_path.write_text(json.dumps(manifest, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    print(f"[manifest] Wrote {out_path}: {json.dumps(manifest['counts'], ensure_ascii=False)}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
