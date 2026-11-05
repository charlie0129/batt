"""MkDocs hook that builds the site's home page from README.md.

README.md is the only copy of the landing page. GitHub renders it as is; for
the site, this hook rewrites the repository-relative docs/ paths and turns
GitHub-only markdown into its Material for MkDocs equivalent:

- GitHub alerts (> [!NOTE]) become admonitions; a leading bold line becomes
  the title.
- shields.io badge links become Material buttons, with an icon when the
  label is in BUTTON_ICONS.
- Headings between <!-- tabs:start --> and <!-- tabs:end --> become content
  tabs.
- Anything between <!-- github-only:start --> and <!-- github-only:end -->
  is dropped.
"""

import re
from pathlib import Path

from mkdocs.structure.files import File

GITHUB_ONLY = re.compile(r"<!-- github-only:start -->.*?<!-- github-only:end -->\n?", re.S)
BADGE = re.compile(r"\[!\[([^\]]+)\]\(https://img\.shields\.io/[^)]*\)\]\(([^)]+)\)")
ALERT = re.compile(r"> \[!(NOTE|TIP|IMPORTANT|WARNING|CAUTION)\]\s*")
ADMONITION_TYPES = {"NOTE": "note", "TIP": "tip", "IMPORTANT": "info", "WARNING": "warning", "CAUTION": "danger"}
TITLE = re.compile(r"\*\*(.+?)\*\*\s*(<br>)?\s*")
TABS = re.compile(r"<!-- tabs:start -->\n(.*?)<!-- tabs:end -->\n?", re.S)
HEADING = re.compile(r"#{2,6} (.+)")
BUTTON_ICONS = {
    "Installation": "fontawesome-solid-book-bookmark",
    "Compatibility": "fontawesome-solid-book-bookmark",
    "Usage": "fontawesome-solid-book-bookmark",
    "Building": "fontawesome-solid-book-bookmark",
    "FAQ": "fontawesome-solid-circle-question",
    "Comparison": "fontawesome-solid-face-grin-stars",
    "Releases": "fontawesome-solid-download",
    "Homebrew": "fontawesome-solid-beer-mug-empty",
    "GitHub": "fontawesome-brands-github",
    "Full Installation Guide": "fontawesome-solid-book-bookmark",
    "Full Compatibility Guide": "fontawesome-solid-book-bookmark",
    "See the full usage Guide here": "fontawesome-solid-book-bookmark",
    "Full Building Guide": "fontawesome-solid-hammer",
}


def convert_alerts(text: str) -> str:
    lines, out, i = text.split("\n"), [], 0
    while i < len(lines):
        alert = ALERT.fullmatch(lines[i])
        if not alert:
            out.append(lines[i])
            i += 1
            continue
        i += 1
        body = []
        while i < len(lines) and lines[i].startswith(">"):
            body.append(lines[i][2:] if lines[i].startswith("> ") else lines[i][1:])
            i += 1
        title = TITLE.fullmatch(body[0]) if body else None
        if title:
            body = body[1:]
        out.append(f"!!! {ADMONITION_TYPES[alert.group(1)]}" + (f' "{title.group(1)}"' if title else ""))
        out.extend(f"    {line}" if line.strip() else "" for line in body)
    return "\n".join(out)


def convert_tabs(match: re.Match) -> str:
    out = []
    for line in match.group(1).rstrip("\n").split("\n"):
        heading = HEADING.fullmatch(line)
        if heading:
            out.append(f'=== "{heading.group(1)}"')
        else:
            out.append(f"    {line}" if line.strip() else "")
    return "\n".join(out) + "\n"


def badge_to_button(match: re.Match) -> str:
    label = match.group(1).replace("-", " ")
    icon = BUTTON_ICONS.get(label)
    text = f":{icon}: {label}" if icon else label
    return f"[{text}]({match.group(2)}){{ .md-button .md-button--primary }}"


def readme_for_site(text: str) -> str:
    text = GITHUB_ONLY.sub("", text)
    text = TABS.sub(convert_tabs, text)
    text = text.replace("](docs/", "](").replace('src="docs/', 'src="')
    text = BADGE.sub(badge_to_button, text)
    return convert_alerts(text)


def on_files(files, config):
    readme = Path(config["config_file_path"]).parent / "README.md"
    files.append(File.generated(config, "index.md", content=readme_for_site(readme.read_text(encoding="utf-8"))))
    return files
