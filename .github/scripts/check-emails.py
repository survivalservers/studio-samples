"""Fail when a commit or a package file carries a personal email address.

Commits: every author and committer address must be a GitHub no-reply address (…@users.noreply.github.com).
The one other committer allowed is GitHub's own merge identity, "GitHub <noreply@github.com>", which GitHub
writes as the committer of every squash merge.
Package files: an author, maintainer or owner field may hold no email, a no-reply address or the role address
studio-packages@survivalservers.com, and nothing else.

Usage: python3 check-emails.py <first commit to check>..<last commit> (or a single commit)
"""

import json
import pathlib
import re
import subprocess
import sys
import tomllib

NOREPLY = re.compile(r"^[A-Za-z0-9._+-]+@users\.noreply\.github\.com$")
GITHUB_MERGE = ("GitHub", "noreply@github.com")
ROLE = {"studio-packages@survivalservers.com"}
EMAIL = re.compile(r"[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}")

problems = []


def check_commits(rev_range):
    out = subprocess.run(
        ["git", "log", "--format=%H%x1f%an%x1f%ae%x1f%cn%x1f%ce", rev_range],
        check=True, capture_output=True, text=True,
    ).stdout
    count = 0
    for line in out.splitlines():
        sha, an, ae, cn, ce = line.split("\x1f")
        count += 1
        if not NOREPLY.match(ae):
            problems.append(f"commit {sha[:12]}: author address is not a GitHub no-reply address")
        if not (NOREPLY.match(ce) or (cn, ce) == GITHUB_MERGE):
            problems.append(f"commit {sha[:12]}: committer address is not a GitHub no-reply address")
    print(f"checked {count} commit(s)")


def allowed(address):
    return bool(NOREPLY.match(address)) or address.lower() in ROLE


def check_people(path, field, value):
    # value can be a string ("Name <email>"), a dict ({"name":..., "email":...}) or a list of either
    items = value if isinstance(value, list) else [value]
    for item in items:
        text = json.dumps(item) if isinstance(item, dict) else str(item)
        for address in EMAIL.findall(text):
            if not allowed(address):
                problems.append(f"{path}: {field} holds an address that is not a role or no-reply address")


def check_files():
    files = subprocess.run(["git", "ls-files"], check=True, capture_output=True, text=True).stdout.split()
    for name in files:
        path = pathlib.Path(name)
        if path.name == "package.json":
            data = json.loads(path.read_text())
            for field in ("author", "maintainers", "contributors"):
                if field in data:
                    check_people(name, field, data[field])
        elif path.name == "pyproject.toml":
            project = tomllib.loads(path.read_text()).get("project", {})
            for field in ("authors", "maintainers"):
                if field in project:
                    check_people(name, field, project[field])
        elif path.name == "Cargo.toml":
            package = tomllib.loads(path.read_text()).get("package", {})
            if "authors" in package:
                check_people(name, "authors", package["authors"])
        elif path.suffix in (".nuspec", ".csproj"):
            text = path.read_text()
            for tag in ("authors", "owners", "Authors", "Company"):
                for value in re.findall(rf"<{tag}>(.*?)</{tag}>", text, flags=re.S):
                    check_people(name, tag, value)
    print(f"checked {len(files)} file(s)")


check_commits(sys.argv[1])
check_files()
if problems:
    print("Personal email addresses found (addresses are not printed):")
    for p in problems:
        print(" -", p)
    sys.exit(1)
print("OK: no personal email addresses")
