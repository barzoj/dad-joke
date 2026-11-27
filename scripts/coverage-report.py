#!/usr/bin/env python3
"""Report Go coverage for CI: filter gen/, gate internal/, emit summary + badge."""

from __future__ import annotations

import argparse
import json
import sys
from collections import defaultdict
from dataclasses import dataclass
from pathlib import Path
from typing import Iterable


@dataclass(frozen=True)
class Block:
    file: str
    statements: int
    covered: int


def normalize_path(path: str) -> str:
    return path.replace("\\", "/")


def is_gen_path(path: str) -> bool:
    p = normalize_path(path).lstrip("./")
    return p == "gen" or p.startswith("gen/") or "/gen/" in p


def is_internal_path(path: str) -> bool:
    return "/internal/" in normalize_path(path)


def parse_profile(path: Path) -> list[Block]:
    text = path.read_text(encoding="utf-8")
    lines = text.splitlines()
    if not lines:
        raise ValueError(f"empty coverage profile: {path}")
    # First line is mode: set|count|atomic
    blocks: list[Block] = []
    for raw in lines[1:]:
        line = raw.strip()
        if not line:
            continue
        # file:startLine.startCol,endLine.endCol numStmt count
        try:
            meta, num_stmt_s, count_s = line.rsplit(" ", 2)
            file_path = meta.split(":", 1)[0]
            num_stmt = int(num_stmt_s)
            count = int(count_s)
        except ValueError as exc:
            raise ValueError(f"malformed coverage line: {raw!r}") from exc
        covered = num_stmt if count > 0 else 0
        blocks.append(Block(file=file_path, statements=num_stmt, covered=covered))
    return blocks


def filter_blocks(blocks: Iterable[Block]) -> list[Block]:
    return [b for b in blocks if not is_gen_path(b.file)]


def aggregate(blocks: Iterable[Block]) -> tuple[int, int]:
    stmts = 0
    covered = 0
    for b in blocks:
        stmts += b.statements
        covered += b.covered
    return covered, stmts


def percent(covered: int, stmts: int) -> float:
    # Zero statements is not "100% covered" — callers treat stmts==0 as a gate failure.
    if stmts == 0:
        return 0.0
    return 100.0 * covered / stmts


def format_pct(value: float) -> str:
    return f"{value:.1f}%"


def badge_color(pct_value: float) -> str:
    if pct_value >= 90:
        return "brightgreen"
    if pct_value >= 80:
        return "green"
    if pct_value >= 70:
        return "yellowgreen"
    if pct_value >= 60:
        return "yellow"
    if pct_value >= 50:
        return "orange"
    return "red"


def file_coverage(blocks: Iterable[Block]) -> dict[str, tuple[int, int]]:
    by_file: dict[str, list[int]] = defaultdict(lambda: [0, 0])
    for b in blocks:
        by_file[b.file][0] += b.covered
        by_file[b.file][1] += b.statements
    return {f: (c, s) for f, (c, s) in by_file.items()}


def match_changed_file(changed: str, profile_files: Iterable[str]) -> str | None:
    """Map a repo-relative path to a coverage profile path when possible."""
    changed_n = normalize_path(changed).lstrip("./")
    if is_gen_path(changed_n):
        return None
    for profile in profile_files:
        p = normalize_path(profile)
        # Require a path boundary before the suffix so foointernal/... cannot match
        # a changed path of internal/...
        if p == changed_n or p.endswith("/" + changed_n):
            return profile
    return None


def render_markdown(
    *,
    total_pct: float,
    internal_pct: float,
    internal_min: float,
    changed_rows: list[tuple[str, str]],
    threshold_ok: bool,
) -> str:
    lines = [
        "## Coverage report",
        "",
        f"- **Total** (excluding `/gen/`): {format_pct(total_pct)}",
        f"- **`internal/`**: {format_pct(internal_pct)} (minimum {format_pct(internal_min)})",
        f"- **Threshold**: {'pass' if threshold_ok else 'FAIL'}",
        "",
        "### Changed `.go` files",
        "",
    ]
    if not changed_rows:
        lines.append("_No changed `.go` files in this run (or none matched the profile)._")
    else:
        lines.extend(
            [
                "| File | Coverage |",
                "| --- | ---: |",
            ]
        )
        for path, cov in changed_rows:
            lines.append(f"| `{path}` | {cov} |")
    lines.append("")
    return "\n".join(lines)


def write_badge(path: Path, total_pct: float) -> None:
    payload = {
        "schemaVersion": 1,
        "label": "coverage",
        "message": format_pct(total_pct),
        "color": badge_color(total_pct),
    }
    path.write_text(json.dumps(payload, indent=2) + "\n", encoding="utf-8")


def read_changed_files(path: Path | None, inline: list[str]) -> list[str]:
    files: list[str] = []
    if path is not None:
        for line in path.read_text(encoding="utf-8").splitlines():
            item = line.strip()
            if item and not is_gen_path(item):
                files.append(item)
    for item in inline:
        item = item.strip()
        if item and not is_gen_path(item):
            files.append(item)
    # de-dupe, keep order
    seen: set[str] = set()
    out: list[str] = []
    for f in files:
        if f not in seen:
            seen.add(f)
            out.append(f)
    return out


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--profile", type=Path, required=True, help="go coverprofile path")
    parser.add_argument(
        "--internal-min",
        type=float,
        default=70.0,
        help="minimum required coverage percent for internal/ (default: 70)",
    )
    parser.add_argument("--summary", type=Path, help="append markdown to this file (GITHUB_STEP_SUMMARY)")
    parser.add_argument("--comment", type=Path, help="write sticky PR comment markdown here")
    parser.add_argument("--badge", type=Path, help="write shields.io endpoint JSON here")
    parser.add_argument(
        "--changed-files",
        type=Path,
        help="file listing changed .go paths (one per line, repo-relative)",
    )
    parser.add_argument(
        "changed",
        nargs="*",
        help="optional changed .go paths (repo-relative)",
    )
    args = parser.parse_args(argv)

    try:
        blocks = filter_blocks(parse_profile(args.profile))
    except ValueError as exc:
        print(f"coverage error: {exc}", file=sys.stderr)
        return 1

    total_covered, total_stmts = aggregate(blocks)
    internal_blocks = [b for b in blocks if is_internal_path(b.file)]
    internal_covered, internal_stmts = aggregate(internal_blocks)

    total_pct = percent(total_covered, total_stmts)
    internal_pct = percent(internal_covered, internal_stmts)
    if internal_stmts == 0:
        threshold_ok = False
        print(
            "coverage error: no internal/ statements in coverprofile "
            "(empty, mode-only, or gen/-only profile); refusing to treat as 100%",
            file=sys.stderr,
        )
    else:
        threshold_ok = internal_pct + 1e-9 >= args.internal_min

    by_file = file_coverage(blocks)
    changed = read_changed_files(args.changed_files, args.changed)
    changed_rows: list[tuple[str, str]] = []
    for rel in changed:
        if not rel.endswith(".go"):
            continue
        matched = match_changed_file(rel, by_file.keys())
        if matched is None:
            # Missing from the profile: deleted, Windows-only, or never instrumented.
            changed_rows.append((rel, "n/a"))
            continue
        c, s = by_file[matched]
        changed_rows.append((rel, format_pct(percent(c, s))))

    md = render_markdown(
        total_pct=total_pct,
        internal_pct=internal_pct,
        internal_min=args.internal_min,
        changed_rows=changed_rows,
        threshold_ok=threshold_ok,
    )

    print(
        f"coverage total={format_pct(total_pct)} "
        f"internal={format_pct(internal_pct)} "
        f"min={format_pct(args.internal_min)} "
        f"ok={threshold_ok}"
    )
    print(md)

    if args.summary is not None:
        with args.summary.open("a", encoding="utf-8") as fh:
            fh.write(md)
            if not md.endswith("\n"):
                fh.write("\n")

    if args.comment is not None:
        args.comment.write_text(md, encoding="utf-8")

    if args.badge is not None:
        write_badge(args.badge, total_pct)

    return 0 if threshold_ok else 1


if __name__ == "__main__":
    sys.exit(main())
