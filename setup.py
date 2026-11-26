"""Custom setuptools build hook: builds librulemma shared library via Go if available and bundles data/."""
from __future__ import annotations

import platform
import shutil
import subprocess
from pathlib import Path
from setuptools import setup
from setuptools.command.build_py import build_py as _build_py

ROOT = Path(__file__).resolve().parent
PKG_DIR = ROOT / "python" / "rulemma"


def shared_lib_name() -> str:
    sys_name = platform.system()
    if sys_name == "Darwin":
        return "librulemma.dylib"
    if sys_name == "Windows":
        return "rulemma.dll"
    return "librulemma.so"


class build_py(_build_py):
    def run(self) -> None:
        lib_path = PKG_DIR / shared_lib_name()
        go_bin = shutil.which("go")
        if go_bin and (ROOT / "capi" / "capi.go").exists():
            subprocess.check_call(
                [go_bin, "build", "-buildmode=c-shared", "-o", str(lib_path), "./capi"],
                cwd=str(ROOT),
            )
            # Remove generated C header from python package dir
            hdr = lib_path.with_suffix(".h")
            if hdr.exists():
                hdr.unlink()
        # Copy compact data files into package dir for wheel distribution
        pkg_data = PKG_DIR / "data"
        pkg_data.mkdir(exist_ok=True)
        for fname in ("lemmas.tsv.gz", "adj_forms.tsv.gz", "types.tsv.gz", "adj_noun.tsv.gz", "overrides.tsv"):
            src = ROOT / "data" / fname
            if src.exists():
                shutil.copy2(src, pkg_data / fname)
        super().run()


setup(cmdclass={"build_py": build_py})
