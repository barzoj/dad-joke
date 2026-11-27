#!/usr/bin/env python3
"""Unit tests for coverage-report.py (stdlib unittest)."""

from __future__ import annotations

import importlib.util
import tempfile
import unittest
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
            cov = root / "coverage.out"
            cov.write_text(profile, encoding="utf-8")
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
            cov = Path(tmp) / "coverage.out"
            cov.write_text(profile, encoding="utf-8")
            rc = self.mod.main(
                ["--profile", str(cov), "--internal-min", "70"]
            )
            self.assertEqual(rc, 1)


if __name__ == "__main__":
    unittest.main()
