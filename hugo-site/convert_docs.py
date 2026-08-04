#!/usr/bin/env python3
"""Convert MkDocs Markdown docs to Hugo Relearn format.

Transformations:
  - Add Hugo front matter (title, weight) from H1 or explicit map
  - Convert MkDocs admonitions (!!! type "title") to GitHub/Obsidian alerts (> [!type] title)
  - Rename index.md → _index.md for section roots
  - Strip MkDocs-specific front matter (hide:)
"""
import os
import re
import shutil
import sys

SRC = "docs"
DST = "hugo-site/content"

# Menu ordering per language (matches mkdocs.yml nav)
PAGE_WEIGHTS = {
    "index": 0,
    "features": 1,
    "quickstart": 2,
    "concepts": 10,
    "architecture": 11,
    "components": 12,
    "eip": 13,
    "simple-language": 14,
    "installation": 20,
    "configuration": 21,
    "examples": 22,
    "reference": 30,
}

# MkDocs admonition type → GitHub alert type
ADMONITION_MAP = {
    "note": "note",
    "abstract": "note",
    "info": "note",
    "todo": "note",
    "tip": "tip",
    "success": "tip",
    "question": "tip",
    "warning": "warning",
    "failure": "warning",
    "danger": "caution",
    "bug": "caution",
    "example": "note",
    "quote": "note",
    "cite": "note",
}

# MkDocs front matter to strip (lines between --- markers)
MKDOCS_FM_RE = re.compile(r"^---\n.*?\n---\n", re.DOTALL)


def convert_admonitions(text: str) -> str:
    """Convert MkDocs !!! admonitions to GitHub/Obsidian callout syntax."""
    lines = text.split("\n")
    result = []
    i = 0
    while i < len(lines):
        line = lines[i]
        # Match: !!! type "optional title"
        # Also match: !!! type (no title)
        m = re.match(r'^!!!\s+(\w+)(?:\s+"([^"]*)")?\s*$', line)
        if m:
            adm_type = m.group(1).lower()
            title = m.group(2) or ""
            alert_type = ADMONITION_MAP.get(adm_type, "note")

            # Collect indented body lines (4 spaces or tab)
            body = []
            i += 1
            while i < len(lines):
                bl = lines[i]
                if bl.startswith("    ") or bl.startswith("\t"):
                    # Strip 4-space indent (or tab)
                    if bl.startswith("    "):
                        body.append(bl[4:])
                    else:
                        body.append(bl[1:])
                    i += 1
                elif bl.strip() == "":
                    # Empty line — could be inside admonition if next line is indented
                    # Look ahead
                    if i + 1 < len(lines) and (lines[i + 1].startswith("    ") or lines[i + 1].startswith("\t")):
                        body.append("")
                        i += 1
                    else:
                        break
                else:
                    break

            # Build the callout
            if title:
                result.append(f"> [!{alert_type}] {title}")
            else:
                result.append(f"> [!{alert_type}]")
            for bl in body:
                if bl.strip():
                    result.append(f"> {bl}")
                else:
                    result.append(">")
            # Ensure a blank line after the callout
            result.append("")
        else:
            result.append(line)
            i += 1

    return "\n".join(result)


def extract_title(text: str) -> str:
    """Extract the H1 title from markdown."""
    m = re.search(r"^#\s+(.+)$", text, re.MULTILINE)
    if m:
        return m.group(1).strip()
    return "Untitled"


def strip_mkdocs_frontmatter(text: str) -> str:
    """Remove MkDocs front matter (--- blocks at file start)."""
    return MKDOCS_FM_RE.sub("", text, count=1)


def strip_h1(text: str) -> str:
    """Remove the first H1 (title is in front matter)."""
    return re.sub(r"^#\s+.+\n+", "", text, count=1, flags=re.MULTILINE)


def convert_file(src_path: str, dst_path: str, lang: str, filename: str):
    """Convert a single markdown file."""
    with open(src_path, "r") as f:
        content = f.read()

    # Strip MkDocs front matter if present
    content = strip_mkdocs_frontmatter(content)

    # Extract title
    title = extract_title(content)

    # Remove H1 (title goes in front matter)
    content = strip_h1(content)

    # Convert admonitions
    content = convert_admonitions(content)

    # Get weight
    stem = filename.replace(".md", "")
    weight = PAGE_WEIGHTS.get(stem, 50)

    # Build front matter
    # _index.md files are section pages (branch bundles)
    is_index = stem == "index"

    fm_lines = [
        "+++",
        f'title = "{title}"',
        f"weight = {weight}",
    ]
    if is_index:
        fm_lines.append("[build]")
        fm_lines.append('  list = "always"')
    fm_lines.append("+++")
    fm_lines.append("")

    # Write output
    with open(dst_path, "w") as f:
        f.write("\n".join(fm_lines) + content)


def main():
    # With contentDir per language (hugo.toml), each language's content
    # lives in content/<lang>/. Hugo maps content/en/ → /en/ and
    # content/fr/ → /fr/. No duplication.
    for lang in ["en", "fr"]:
        os.makedirs(os.path.join(DST, lang), exist_ok=True)
    # Convert EN docs → content/en/
    en_dir = os.path.join(SRC, "en")
    for filename in sorted(os.listdir(en_dir)):
        if not filename.endswith(".md"):
            continue
        src = os.path.join(en_dir, filename)
        dst_name = "_index.md" if filename == "index.md" else filename
        dst = os.path.join(DST, "en", dst_name)
        convert_file(src, dst, "en", filename)
        print(f"  EN: {filename} → en/{dst_name}")

    # Convert FR docs → content/fr/
    fr_dir = os.path.join(SRC, "fr")
    for filename in sorted(os.listdir(fr_dir)):
        if not filename.endswith(".md"):
            continue
        src = os.path.join(fr_dir, filename)
        dst_name = "_index.md" if filename == "index.md" else filename
        dst = os.path.join(DST, "fr", dst_name)
        convert_file(src, dst, "fr", filename)
        print(f"  FR: {filename} → fr/{dst_name}")

    print("\nConversion complete!")