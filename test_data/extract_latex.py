#!/usr/bin/env python3
"""Extract every unique LaTeX math expression from .md files under ave-kb/.

Scans for:
  - Inline math:  $...$  (not $$)
  - Display math: $$...$$
  - Fenced math:  ```math ... ```

Outputs deduplicated expressions to torture_test.md, grouped by type.
"""

import os
import re
import sys
from pathlib import Path

SRC = Path.home() / "projects/Applied-Vacuum-Engineering/manuscript/ave-kb"
DST = Path(__file__).parent / "torture_test.md"

# Patterns — order matters: grab $$ before $
DISPLAY_RE = re.compile(r'\$\$(.+?)\$\$', re.DOTALL)
INLINE_RE  = re.compile(r'(?<!\$)\$(?!\$)(.+?)(?<!\$)\$(?!\$)')
FENCE_RE   = re.compile(r'```math\s*\n(.*?)```', re.DOTALL)


def extract_from_file(path: Path) -> tuple[set[str], set[str]]:
    """Return (inline_exprs, display_exprs) found in one file."""
    try:
        text = path.read_text(encoding="utf-8", errors="replace")
    except OSError:
        return set(), set()

    display = set()
    inline  = set()

    # Fenced math blocks → display
    for m in FENCE_RE.finditer(text):
        expr = m.group(1).strip()
        if expr:
            display.add(expr)

    # Remove fenced blocks so they don't interfere with $ matching
    text_no_fence = FENCE_RE.sub('', text)

    # Remove code blocks (``` ... ```) so we don't pick up $ inside code
    text_no_code = re.sub(r'```.*?```', '', text_no_fence, flags=re.DOTALL)

    # Remove inline code (`...`)
    text_clean = re.sub(r'`[^`]+`', '', text_no_code)

    # $$ display math
    for m in DISPLAY_RE.finditer(text_clean):
        expr = m.group(1).strip()
        if expr:
            display.add(expr)

    # Remove $$ so they don't match as two inline $
    text_no_display = DISPLAY_RE.sub('', text_clean)

    # $ inline math
    for m in INLINE_RE.finditer(text_no_display):
        expr = m.group(1).strip()
        if expr and not expr.startswith(('/', 'http', '#')):
            inline.add(expr)

    return inline, display


def main():
    if not SRC.is_dir():
        print(f"Source directory not found: {SRC}", file=sys.stderr)
        sys.exit(1)

    all_inline  = set()
    all_display = set()
    file_count  = 0

    for md in sorted(SRC.rglob("*.md")):
        inline, display = extract_from_file(md)
        all_inline  |= inline
        all_display |= display
        file_count  += 1

    # Sort for stable output
    inline_sorted  = sorted(all_inline)
    display_sorted = sorted(all_display)

    with open(DST, "w", encoding="utf-8") as f:
        f.write("# LaTeX Torture Test Expressions\n\n")
        f.write(f"Extracted from {file_count} .md files under `ave-kb/`.\n\n")
        f.write(f"**{len(inline_sorted)}** unique inline expressions, "
                f"**{len(display_sorted)}** unique display expressions.\n\n")

        f.write("## Inline Math\n\n")
        for i, expr in enumerate(inline_sorted, 1):
            f.write(f"{i}. `${expr}$`\n")

        f.write(f"\n## Display Math\n\n")
        for i, expr in enumerate(display_sorted, 1):
            f.write(f"{i}. ```\n{expr}\n```\n\n")

    print(f"Scanned {file_count} files")
    print(f"  Inline:  {len(inline_sorted)} unique expressions")
    print(f"  Display: {len(display_sorted)} unique expressions")
    print(f"  Written: {DST}")


if __name__ == "__main__":
    main()
