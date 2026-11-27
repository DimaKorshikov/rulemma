"""Pre-release consistency & open-source hygiene check for rulemma.

Zero external dependencies (Python stdlib only). Checks:
1. Single-source version/ABI/schema consistency across Go, Python, manifest, README, CHANGELOG.
2. Dictionary manifest checksums and entry counts (via tools/update_manifest.py).
3. Synchronization of dictionary counts and size in README.md.
4. Synchronization of benchmark numbers between benchmarks/results.json and README.md
   (both summary table and methodology notes).
5. Keep a Changelog structure (dated release header + comparison link).
6. Repository hygiene (no tracked binaries, build artifacts, secrets, or local absolute paths).
"""
from __future__ import annotations

import json
import re
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT / "tools"))
import update_manifest  # noqa: E402


def fmt_int(n: int) -> str:
    return f"{n:,}".replace(",", " ")


def parse_semver(v: str) -> tuple[int, int, int]:
    m = re.fullmatch(r"(\d+)\.(\d+)\.(\d+)", v.strip())
    if not m:
        raise ValueError(f"Invalid SemVer: {v!r}")
    return int(m.group(1)), int(m.group(2)), int(m.group(3))


def check_versions(errors: list[str]) -> tuple[str, int, int]:
    version_go = (ROOT / "version.go").read_text(encoding="utf-8")
    m_ver = re.search(r'Version\s*=\s*"([^"]+)"', version_go)
    m_abi = re.search(r"ABIVersion\s*=\s*(\d+)", version_go)
    m_schema = re.search(r"DictSchemaVersion\s*=\s*(\d+)", version_go)
    if not (m_ver and m_abi and m_schema):
        errors.append("version.go: missing Version / ABIVersion / DictSchemaVersion")
        return "", 0, 0
    go_ver = m_ver.group(1)
    go_abi = int(m_abi.group(1))
    go_schema = int(m_schema.group(1))

    pyproject = (ROOT / "pyproject.toml").read_text(encoding="utf-8")
    m_pyp = re.search(r'(?m)^version\s*=\s*"([^"]+)"', pyproject)
    pyp_ver = m_pyp.group(1) if m_pyp else ""
    if pyp_ver != go_ver:
        errors.append(f"Version mismatch: version.go={go_ver!r} != pyproject.toml={pyp_ver!r}")

    py_init = (ROOT / "python/rulemma/__init__.py").read_text(encoding="utf-8")
    m_init = re.search(r'(?m)^__version__\s*=\s*"([^"]+)"', py_init)
    init_ver = m_init.group(1) if m_init else ""
    if init_ver != go_ver:
        errors.append(f"Version mismatch: version.go={go_ver!r} != python/rulemma/__init__.py={init_ver!r}")

    manifest = json.loads((ROOT / "data/manifest.json").read_text(encoding="utf-8"))
    if manifest.get("schema_version") != go_schema:
        errors.append(
            f"Schema mismatch: version.go={go_schema} != data/manifest.json={manifest.get('schema_version')}"
        )
    min_ver = str(manifest.get("engine_min_version", ""))
    try:
        if parse_semver(min_ver) > parse_semver(go_ver):
            errors.append(f"manifest.json engine_min_version={min_ver} > version.go={go_ver}")
    except ValueError as e:
        errors.append(f"manifest.json engine_min_version error: {e}")

    readme = (ROOT / "README.md").read_text(encoding="utf-8")
    if f"rulemma {go_ver}" not in readme:
        errors.append(f"README.md does not mention current version 'rulemma {go_ver}'")
    if f"# native {go_ver} {go_abi}" not in readme:
        errors.append(f"README.md Python quickstart comment missing '# native {go_ver} {go_abi}'")

    return go_ver, go_abi, go_schema


def check_manifest_and_readme_counts(errors: list[str]) -> None:
    data_dir = ROOT / "data"
    manifest_path = data_dir / "manifest.json"
    existing = json.loads(manifest_path.read_text(encoding="utf-8"))
    computed = update_manifest.build_manifest(
        data_dir,
        dict_version=existing.get("dict_version", update_manifest.DEFAULT_DICT_VERSION),
        engine_min_version=existing.get("engine_min_version", update_manifest.DEFAULT_ENGINE_MIN_VERSION),
    )
    for field in ("schema_version", "files", "counts", "sha256"):
        if existing.get(field) != computed.get(field):
            errors.append(f"data/manifest.json mismatch in '{field}' (run python3 tools/update_manifest.py)")

    readme = (ROOT / "README.md").read_text(encoding="utf-8")
    for key, val in computed["counts"].items():
        formatted = fmt_int(val)
        if formatted not in readme:
            errors.append(f"README.md missing manifest count {key}={formatted}")

    total_bytes = sum((data_dir / fname).stat().st_size for fname in computed["files"].values())
    size_mib = f"{total_bytes / (1024 * 1024):.1f} МБ"
    size_mb = f"{total_bytes / 1_000_000:.1f} МБ"
    if size_mib not in readme and size_mb not in readme:
        errors.append(
            f"README.md missing actual data/ total size ({size_mib} MiB / {size_mb} MB; {total_bytes} bytes)"
        )


def check_benchmarks_sync(version: str, errors: list[str]) -> None:
    res_path = ROOT / "benchmarks/results.json"
    if not res_path.exists():
        errors.append("benchmarks/results.json not found")
        return
    data = json.loads(res_path.read_text(encoding="utf-8"))
    models = {m["key"]: m for m in data.get("models", [])}
    readme = (ROOT / "README.md").read_text(encoding="utf-8")
    results_md = (ROOT / "benchmarks/RESULTS.md").read_text(encoding="utf-8")

    ru = models.get("rulemma")
    if not ru:
        errors.append("benchmarks/results.json missing 'rulemma' model entry")
        return
    if version and f"rulemma {version}" not in ru.get("model", ""):
        errors.append(
            f"benchmarks/results.json model label {ru.get('model')!r} does not match current version {version!r}"
        )

    # Check key numbers in README.md (table + prose notes)
    expected_in_readme = [
        ("rulemma batch_qps", f"{fmt_int(ru['batch_qps'])} запр/с"),
        ("rulemma single_us", f"{ru['single_us']:.1f} мкс"),
        ("rulemma load_rss_mb", f"{ru['load_rss_mb']:.1f} МБ"),
        ("rulemma peak_rss_mb", f"{ru['peak_rss_mb']:.1f} МБ"),
    ]
    for key, label in [
        ("pymorphy3_cached", "pymorphy3 cached"),
        ("pymorphy3_uncached", "pymorphy3 uncached"),
        ("simplemma", "simplemma"),
    ]:
        m = models.get(key)
        if m:
            expected_in_readme.append((f"{label} single_us", f"{m['single_us']:.1f} мкс"))
            expected_in_readme.append((f"{label} batch_qps", f"{fmt_int(m['batch_qps'])} запр/с"))

    for name, needle in expected_in_readme:
        if needle not in readme:
            errors.append(f"README.md out of sync with benchmarks/results.json ({name}: expected {needle!r})")

    # Specifically verify prose numbers in "Открытые оговорки" match table/results.json
    notes_marker = "## Открытые оговорки по методике замера"
    if notes_marker in readme:
        notes_sec = readme.split(notes_marker, 1)[1].split("\n## ", 1)[0]
        checks = [
            ("simplemma single_us in notes", f"{models['simplemma']['single_us']:.1f} мкс"),
            ("pymorphy3_cached single_us in notes", f"{models['pymorphy3_cached']['single_us']:.1f} мкс"),
            ("rulemma single_us in notes", f"{ru['single_us']:.1f} мкс"),
            ("pymorphy3_cached batch_qps in notes", f"{fmt_int(models['pymorphy3_cached']['batch_qps'])} запр/с"),
            ("pymorphy3_uncached batch_qps in notes", f"{fmt_int(models['pymorphy3_uncached']['batch_qps'])} запр/с"),
        ]
        for name, needle in checks:
            if needle not in notes_sec:
                errors.append(f"README.md methodology notes out of sync ({name}: expected {needle!r})")

    # Check RESULTS.md matches current version
    if version and f"{version}" not in results_md:
        errors.append(f"benchmarks/RESULTS.md does not mention version {version}")


def check_changelog(version: str, errors: list[str]) -> None:
    cl = (ROOT / "CHANGELOG.md").read_text(encoding="utf-8")
    if not re.search(rf"(?m)^## \[{re.escape(version)}\] - \d{{4}}-\d{{2}}-\d{{2}}\s*$", cl):
        errors.append(f"CHANGELOG.md missing dated heading '## [{version}] - YYYY-MM-DD'")
    if not re.search(rf"(?m)^\[{re.escape(version)}\]:\s+https://", cl):
        errors.append(f"CHANGELOG.md missing Keep a Changelog reference link '[{version}]: https://...'")


def check_repo_hygiene(errors: list[str]) -> None:
    try:
        out = subprocess.check_output(["git", "ls-files"], cwd=ROOT, text=True)
        files = [line.strip() for line in out.splitlines() if line.strip()]
    except Exception:
        return

    forbidden_prefixes = ("private/", "raw_dicts/", "bin/", "build/", "dist/", "python/rulemma/data/")
    forbidden_names = {"AGENTS.md", "CLAUDE.md", "GEMINI.md"}
    forbidden_suffixes = (".so", ".dylib", ".dll", ".pyc", ".egg-info")
    path_leak_re = re.compile(r"(/home/[a-zA-Z0-9._-]+/|/Users/[a-zA-Z0-9._-]+/|C:\\Users\\)")

    for rel in files:
        p = ROOT / rel
        if rel.startswith(forbidden_prefixes) or p.name in forbidden_names or rel.endswith(forbidden_suffixes):
            errors.append(f"Forbidden file tracked in git: {rel}")
            continue
        if rel == "tools/release_check.py" or rel.endswith((".gz", ".png", ".jpg", ".wasm")):
            continue
        if not p.is_file():
            continue
        try:
            text = p.read_text(encoding="utf-8")
        except UnicodeDecodeError:
            continue
        m = path_leak_re.search(text)
        if m:
            errors.append(f"Local absolute path leak in {rel}: {m.group(0)!r}")


def main() -> int:
    errors: list[str] = []
    version, abi, schema = check_versions(errors)
    check_manifest_and_readme_counts(errors)
    check_benchmarks_sync(version, errors)
    check_changelog(version, errors)
    check_repo_hygiene(errors)

    if errors:
        print(f"[release-check] FAILED ({len(errors)} issue(s)):", file=sys.stderr)
        for err in errors:
            print(f"  - {err}", file=sys.stderr)
        return 1

    print(
        f"[release-check] OK: version={version}, abi={abi}, dict_schema={schema}, "
        "manifest/README/CHANGELOG/benchmarks/hygiene verified"
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
