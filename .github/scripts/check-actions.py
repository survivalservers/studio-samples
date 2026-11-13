"""Fail when a workflow, or an action it uses, calls an action that is not on the allow-list.

Reads every `uses:` in .github/workflows/*.yml, then fetches each action's own action.yml at the pinned commit and
follows the `uses:` inside it (composite actions call other actions), all the way down. Every action found must be
pinned to a full 40-character commit and be listed in .github/allowed-actions.txt. Local actions ("./...") are skipped:
GitHub allows them. Prints every action found and where it came from.

Usage: python3 .github/scripts/check-actions.py
"""

import pathlib
import re
import sys
import urllib.error
import urllib.request

USES = re.compile(r"^\s*-?\s*uses:\s*['\"]?([^\s'\"#]+)", re.M)
SHA = re.compile(r"^[0-9a-f]{40}$")

allowed = {
    line.strip()
    for line in pathlib.Path(".github/allowed-actions.txt").read_text().splitlines()
    if line.strip() and not line.startswith("#")
}
problems, seen = [], {}


def fetch_action_yml(owner_repo, path, ref):
    for name in ("action.yml", "action.yaml"):
        url = f"https://raw.githubusercontent.com/{owner_repo}/{ref}/{path + '/' if path else ''}{name}"
        try:
            req = urllib.request.Request(url, headers={"User-Agent": "studio-packages-check/1.0"})
            return urllib.request.urlopen(req, timeout=20).read().decode("utf-8", "replace")
        except urllib.error.HTTPError as e:
            if e.code != 404:
                raise
    return None


def visit(spec, came_from):
    if spec.startswith("./") or spec.startswith("docker://"):
        return
    if spec in seen:
        return
    seen[spec] = came_from
    target, _, ref = spec.partition("@")
    parts = target.split("/")
    owner_repo, path = "/".join(parts[:2]), "/".join(parts[2:])
    if not SHA.match(ref):
        problems.append(f"{spec} (from {came_from}) is not pinned to a full commit")
        return
    if f"{owner_repo}@{ref}" not in allowed:
        problems.append(f"{spec} (from {came_from}) is not on the allow-list")
    text = fetch_action_yml(owner_repo, path, ref)
    if text is None:
        problems.append(f"{spec} (from {came_from}): no action.yml found at that commit")
        return
    for inner in USES.findall(text):
        visit(inner, spec)


for wf in sorted(pathlib.Path(".github/workflows").glob("*.y*ml")):
    for spec in USES.findall(wf.read_text()):
        visit(spec, wf.as_posix())

print("actions found (top level and inside other actions):")
for spec, came_from in seen.items():
    print(f"  {spec}  <- {came_from}")
used = {"/".join(s.split("@")[0].split("/")[:2]) + "@" + s.split("@")[1] for s in seen}
unused = sorted(allowed - used)
if unused:
    print("on the allow-list but not used (fine, but consider removing):", ", ".join(unused))
if problems:
    print("PROBLEMS:")
    for p in problems:
        print("  -", p)
    sys.exit(1)
print("OK: every action, including those called inside other actions, is pinned and allowed")
