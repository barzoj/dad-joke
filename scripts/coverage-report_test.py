#!/usr/bin/env python3
"""Unit tests for coverage-report.py (stdlib unittest)."""

from __future__ import annotations

import importlib.util
import io
import tempfile
import unittest
from contextlib import redirect_stderr
from pathlib import Path


def load_module():
    import sys

    path = Path(__file__).with_name("coverage-report.py")
    spec = importlib.util.spec_from_file_location("coverage_report", path)
    assert spec is not None and spec.loader is not None
    mod = importlib.util.module_from_spec(spec)
    sys.modules[spec.name] = mod
    spec.loader.exec_module(mod)
    return mod


class CoverageReportTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls) -> None:
        cls.mod = load_module()

    def _write_profile(self, root: Path, body: str) -> Path:
        cov = root / "coverage.out"
        cov.write_text(body, encoding="utf-8")
        return cov

    def test_filters_gen_and_computes_internal(self) -> None:
        profile = "\n".join(
            [
                "mode: atomic",
                "github.com/barzoj/dad-joke/internal/cli/cli.go:1.1,2.2 10 1",
                "github.com/barzoj/dad-joke/internal/cli/cli.go:3.1,4.2 10 0",
                "github.com/barzoj/dad-joke/gen/api/api.go:1.1,2.2 100 1",
                "github.com/barzoj/dad-joke/cmd/dad-joke/main.go:1.1,2.2 5 0",
                "",
            ]
        )
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            cov = self._write_profile(root, profile)
            summary = root / "summary.md"
            comment = root / "comment.md"
            badge = root / "badge.json"
            changed = root / "changed.txt"
            changed.write_text(
                "internal/cli/cli.go\ngen/api/api.go\ninternal/missing/missing.go\n",
                encoding="utf-8",
            )

            rc = self.mod.main(
                [
                    "--profile",
                    str(cov),
                    "--internal-min",
                    "40",
                    "--summary",
                    str(summary),
                    "--comment",
                    str(comment),
                    "--badge",
                    str(badge),
                    "--changed-files",
                    str(changed),
                ]
            )
            self.assertEqual(rc, 0)
            text = comment.read_text(encoding="utf-8")
            self.assertIn("internal/", text)
            self.assertIn("50.0%", text)  # internal: 10/20
            self.assertIn("`internal/cli/cli.go`", text)
            self.assertNotIn("`gen/api/api.go`", text)
            self.assertIn("`internal/missing/missing.go`", text)
            self.assertIn("n/a", text)
            badge_json = badge.read_text(encoding="utf-8")
            self.assertIn('"label": "coverage"', badge_json)
            self.assertIn("40.0%", badge_json)  # total excl gen: 10/25

    def test_threshold_failure(self) -> None:
        profile = "\n".join(
            [
                "mode: atomic",
                "github.com/barzoj/dad-joke/internal/cli/cli.go:1.1,2.2 10 1",
                "github.com/barzoj/dad-joke/internal/cli/cli.go:3.1,4.2 90 0",
                "",
            ]
        )
        with tempfile.TemporaryDirectory() as tmp:
            cov = self._write_profile(Path(tmp), profile)
            rc = self.mod.main(["--profile", str(cov), "--internal-min", "70"])
            self.assertEqual(rc, 1)

    def test_empty_changed_files_non_go_pr(self) -> None:
        profile = "\n".join(
            [
                "mode: atomic",
                "github.com/barzoj/dad-joke/internal/cli/cli.go:1.1,2.2 10 1",
                "",
            ]
        )
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            cov = self._write_profile(root, profile)
            changed = root / "changed.txt"
            # Empty list / non-Go PR: only README (ignored) and blank lines.
            changed.write_text("\nREADME.md\ndocs/ARCHITECTURE.md\n", encoding="utf-8")
            comment = root / "comment.md"
            rc = self.mod.main(
                [
                    "--profile",
                    str(cov),
                    "--internal-min",
                    "70",
                    "--comment",
                    str(comment),
                    "--changed-files",
                    str(changed),
                ]
            )
            self.assertEqual(rc, 0)
            text = comment.read_text(encoding="utf-8")
            self.assertIn("No changed `.go` files", text)
            self.assertNotIn("| `README.md`", text)

    def test_mode_only_profile_fails(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            cov = self._write_profile(root, "mode: atomic\n")
            err = io.StringIO()
            with redirect_stderr(err):
                rc = self.mod.main(["--profile", str(cov), "--internal-min", "70"])
            self.assertEqual(rc, 1)
            self.assertIn("no internal/ statements", err.getvalue())

    def test_empty_profile_fails(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            cov = self._write_profile(Path(tmp), "")
            err = io.StringIO()
            with redirect_stderr(err):
                rc = self.mod.main(["--profile", str(cov), "--internal-min", "70"])
            self.assertEqual(rc, 1)
            self.assertIn("empty coverage profile", err.getvalue())

    def test_path_suffix_trap_does_not_match(self) -> None:
        profile_files = [
            "github.com/barzoj/dad-joke/foointernal/cli/cli.go",
            "github.com/barzoj/dad-joke/internal/cli/cli.go",
        ]
        matched = self.mod.match_changed_file("internal/cli/cli.go", profile_files)
        self.assertEqual(matched, "github.com/barzoj/dad-joke/internal/cli/cli.go")

        # Only the trap path present: must not match.
        matched_trap = self.mod.match_changed_file(
            "internal/cli/cli.go",
            ["github.com/barzoj/dad-joke/foointernal/cli/cli.go"],
        )
        self.assertIsNone(matched_trap)

        # endswith(changed_n) would have been True for the trap; boundary check is False.
        trap = "github.com/barzoj/dad-joke/foointernal/cli/cli.go"
        self.assertTrue(trap.endswith("internal/cli/cli.go"))
        self.assertFalse(trap.endswith("/internal/cli/cli.go"))

    def test_deleted_file_shows_na(self) -> None:
        profile = "\n".join(
            [
                "mode: atomic",
                "github.com/barzoj/dad-joke/internal/cli/cli.go:1.1,2.2 10 1",
                "",
            ]
        )
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            cov = self._write_profile(root, profile)
            changed = root / "changed.txt"
            # Deleted (or never-built) Go file still listed in the PR diff.
            changed.write_text(
                "internal/cli/cli.go\ninternal/removed/gone.go\n",
                encoding="utf-8",
            )
            comment = root / "comment.md"
            rc = self.mod.main(
                [
                    "--profile",
                    str(cov),
                    "--internal-min",
                    "70",
                    "--comment",
                    str(comment),
                    "--changed-files",
                    str(changed),
                ]
            )
            self.assertEqual(rc, 0)
            text = comment.read_text(encoding="utf-8")
            self.assertIn("| `internal/cli/cli.go` | 100.0% |", text)
            self.assertIn("| `internal/removed/gone.go` | n/a |", text)


if __name__ == "__main__":
    unittest.main()
