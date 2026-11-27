"""Integration tests verifying that every public example in examples/01..12 runs cleanly."""
from __future__ import annotations

import os
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


if __name__ == "__main__":
    unittest.main()
