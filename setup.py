"""Custom setuptools build hook: builds librulemma shared library via Go if available and bundles data/."""
from __future__ import annotations

import platform
import shutil
import subprocess
from pathlib import Path
from setuptools import setup
from setuptools.command.build_py import build_py as _build_py

try:
    from wheel.bdist_wheel import bdist_wheel as _bdist_wheel
except ImportError:  # pragma: no cover - wheel is optional for editable/sdist builds
    _bdist_wheel = None

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
        super().run()
        # Copy compact data files into build output for wheel/install without polluting source tree
        if not self.editable_mode:
            pkg_data = Path(self.build_lib) / "rulemma" / "data"
            pkg_data.mkdir(parents=True, exist_ok=True)
            for fname in (
                "lemmas.tsv.gz",
                "adj_forms.tsv.gz",
                "types.tsv.gz",
                "adj_noun.tsv.gz",
                "morph.tsv.gz",
                "alt_lemmas.tsv.gz",
                "paradigms.tsv.gz",
                "overrides.tsv",
                "manifest.json",
            ):
                src = ROOT / "data" / fname
                if src.exists():
                    shutil.copy2(src, pkg_data / fname)


cmdclass: dict[str, type] = {"build_py": build_py}
if _bdist_wheel is not None:
    class bdist_wheel(_bdist_wheel):  # type: ignore[misc]
        """Emit a platform-specific wheel tag (e.g. py3-none-linux_x86_64) when librulemma binary is bundled."""

        def finalize_options(self) -> None:
            super().finalize_options()
            if (PKG_DIR / shared_lib_name()).exists() or shutil.which("go"):
                self.root_is_pure = False

        def get_tag(self) -> tuple[str, str, str]:
            python, abi, plat = super().get_tag()
            if not self.root_is_pure:
                return "py3", "none", plat
            return python, abi, plat

    cmdclass["bdist_wheel"] = bdist_wheel


setup(cmdclass=cmdclass)
