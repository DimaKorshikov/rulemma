"""Integration tests verifying public examples (01..13) and API Jupyter notebooks."""
from __future__ import annotations

import json
import os
import re
import shutil
import subprocess
import sys
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
EXAMPLES_DIR = ROOT / "examples"


class TestPublicExamples(unittest.TestCase):
    def test_python_examples_run(self) -> None:
        py_scripts = sorted(EXAMPLES_DIR.glob("[0-9][0-9]_*/main.py"))
        self.assertGreaterEqual(len(py_scripts), 11)
        env = dict(os.environ)
        env["PYTHONPATH"] = str(ROOT / "python")
        for script in py_scripts:
            with self.subTest(example=script.parent.name):
                proc = subprocess.run(
                    [sys.executable, str(script)],
                    cwd=str(ROOT),
                    env=env,
                    capture_output=True,
                    text=True,
                    check=False,
                )
                self.assertEqual(
                    proc.returncode,
                    0,
                    f"{script} failed (rc={proc.returncode}):\nSTDOUT:\n{proc.stdout}\nSTDERR:\n{proc.stderr}",
                )

    def test_go_examples_run(self) -> None:
        go_bin = shutil.which("go") or str(Path.home() / ".local" / "bin" / "go")
        if not Path(go_bin).exists():
            self.skipTest("go binary not available")
        go_dirs = sorted({p.parent for p in EXAMPLES_DIR.glob("[0-9][0-9]_*/main.go")})
        self.assertGreaterEqual(len(go_dirs), 4)
        for d in go_dirs:
            with self.subTest(example=d.name):
                proc = subprocess.run(
                    [go_bin, "run", f"./examples/{d.name}"],
                    cwd=str(ROOT),
                    capture_output=True,
                    text=True,
                    check=False,
                )
                self.assertEqual(
                    proc.returncode,
                    0,
                    f"go run ./examples/{d.name} failed:\nSTDOUT:\n{proc.stdout}\nSTDERR:\n{proc.stderr}",
                )

    def test_cli_pipeline_script(self) -> None:
        bin_path = ROOT / "bin" / "rulemma"
        if not bin_path.exists():
            self.skipTest("CLI binary not built")
        script = EXAMPLES_DIR / "05_go_and_cli_pipeline" / "pipeline.sh"
        proc = subprocess.run(
            ["bash", str(script)],
            cwd=str(ROOT),
            capture_output=True,
            text=True,
            check=False,
        )
        self.assertEqual(proc.returncode, 0, proc.stderr)
        lines = proc.stdout.strip().splitlines()
        self.assertEqual(len(lines), 3)
        self.assertEqual(lines[0], lines[1])
        self.assertIn("см", lines[2])

    def test_jupyter_notebooks_valid(self) -> None:
        nb_dir = EXAMPLES_DIR / "notebooks"
        notebooks = sorted(nb_dir.glob("[0-9][0-9]_*.ipynb"))
        self.assertGreaterEqual(len(notebooks), 5)

        secret_re = re.compile(r"rlm_live_[A-Za-z0-9_-]{12,}")
        for nb_path in notebooks:
            with self.subTest(notebook=nb_path.name):
                data = json.loads(nb_path.read_text(encoding="utf-8"))
                self.assertEqual(data.get("nbformat"), 4)
                cells = data.get("cells", [])
                self.assertGreaterEqual(len(cells), 6)

                # First cell must be a clean Markdown header with registration & Colab links
                first = cells[0]
                self.assertEqual(first.get("cell_type"), "markdown")
                first_src = "".join(first.get("source", []))
                self.assertIn("https://rulemma.ru/account/", first_src)
                self.assertIn("https://rulemma.ru/api/", first_src)
                self.assertIn("https://colab.research.google.com/github/DimaKorshikov/rulemma/", first_src)
                self.assertIn("https://rulemma.ru/v1", first_src)

                code_cells = 0
                for idx, cell in enumerate(cells):
                    src = "".join(cell.get("source", []))
                    self.assertIsNone(secret_re.search(src), f"Hardcoded API key in {nb_path.name} cell {idx}")
                    if cell.get("cell_type") == "code":
                        code_cells += 1
                        self.assertEqual(cell.get("outputs"), [], f"Non-empty outputs in {nb_path.name} cell {idx}")
                        compile(src, f"{nb_path.name}#cell{idx}", "exec")
                self.assertGreaterEqual(code_cells, 4)


if __name__ == "__main__":
    unittest.main()
