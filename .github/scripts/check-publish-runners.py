#!/usr/bin/env python3
"""Fail when a workflow job that publishes can run on a self-hosted runner.

Publishing (npm, PyPI, crates, NuGet, container images, GitHub releases, anything holding the trusted-publisher
id-token) runs only on GitHub's hosted runners. A job counts as publishing when any of these holds:
  - its permissions (or the workflow's, when the job sets none) give id-token: write, or are write-all;
  - its environment is named like a publish step (publish, release, pypi, npm, nuget, crates, registry);
  - a step uses a publishing action, or runs a publishing command (npm/pnpm/yarn publish, twine/uv/poetry publish,
    cargo publish, nuget push, gh release create/upload, docker/oras push, goreleaser, gem push), or asks for the
    id-token itself (ACTIONS_ID_TOKEN_REQUEST).
It can run on a self-hosted runner when its runs-on (or, for an expression, its matrix) names self-hosted or
studio-ci.

Usage: python3 check-publish-runners.py [WORKFLOW_FILE_OR_FOLDER ...]   (default: .github/workflows)
"""
import json
import pathlib
import re
import sys

import yaml

PUBLISH_ACTIONS = re.compile(
    r"^(pypa/gh-action-pypi-publish|softprops/action-gh-release|ncipollo/release-action|actions/create-release|"
    r"actions/upload-release-asset|docker/build-push-action|goreleaser/goreleaser-action|js-devtools/npm-publish|"
    r"changesets/action|rust-lang/crates-io-auth-action|actions/attest-build-provenance)@",
    re.I,
)
PUBLISH_COMMANDS = re.compile(
    r"\b(npm|pnpm|yarn)\s+(publish|release)\b|\btwine\s+upload\b|\b(uv|poetry|flit|hatch)\s+publish\b|"
    r"\bcargo\s+publish\b|\b(dotnet\s+)?nuget\s+push\b|\bgh\s+release\s+(create|upload|edit)\b|"
    r"\b(docker|podman|oras)\s+push\b|\bcrane\s+(push|copy)\b|\bgoreleaser\b|\bgem\s+push\b|"
    r"ACTIONS_ID_TOKEN_REQUEST",
    re.I,
)
PUBLISH_ENVIRONMENT = re.compile(r"publish|release|pypi|npm|nuget|crates|registry", re.I)
OUR_RUNNER = re.compile(r"self-hosted|studio-ci", re.I)


def gives_id_token(perms):
    if isinstance(perms, str):
        return perms.strip().lower() == "write-all"
    if isinstance(perms, dict):
        return str(perms.get("id-token", "")).strip().lower() == "write"
    return False


def why_publishing(job, workflow_perms):
    reasons = []
    perms = job.get("permissions", workflow_perms)
    if gives_id_token(perms):
        reasons.append("id-token: write")
    env = job.get("environment")
    env_name = env.get("name") if isinstance(env, dict) else env
    if isinstance(env_name, str) and PUBLISH_ENVIRONMENT.search(env_name):
        reasons.append(f"environment {env_name}")
    for step in job.get("steps") or []:
        if not isinstance(step, dict):
            continue
        uses = step.get("uses")
        if isinstance(uses, str) and PUBLISH_ACTIONS.search(uses.strip()):
            reasons.append(f"uses {uses.split('@')[0]}")
        run = step.get("run")
        if isinstance(run, str):
            m = PUBLISH_COMMANDS.search(run)
            if m:
                reasons.append(f"runs '{m.group(0)}'")
    return reasons


def runs_on_ours(job):
    runs_on = job.get("runs-on")
    if job.get("uses") and runs_on is None:
        return False  # a reusable workflow: its own file is checked where it lives
    text = yaml.safe_dump(runs_on, default_flow_style=True)
    if "${{" in text:  # an expression: whatever the matrix can make it
        text += yaml.safe_dump((job.get("strategy") or {}).get("matrix"), default_flow_style=True)
    return bool(OUR_RUNNER.search(text))


def check_file(path):
    doc = yaml.safe_load(path.read_text())
    if not isinstance(doc, dict):
        return [], 0
    bad, publishing = [], 0
    for name, job in (doc.get("jobs") or {}).items():
        if not isinstance(job, dict):
            continue
        reasons = why_publishing(job, doc.get("permissions"))
        if not reasons:
            continue
        publishing += 1
        if runs_on_ours(job):
            bad.append(f"{path}: job '{name}' publishes ({', '.join(sorted(set(reasons)))}) and can run on a self-hosted runner "
                       f"(runs-on: {json.dumps(job.get('runs-on'))})")
    return bad, publishing


def main(args):
    paths = []
    for a in args or [".github/workflows"]:
        p = pathlib.Path(a)
        paths += sorted(p.glob("*.yml")) + sorted(p.glob("*.yaml")) if p.is_dir() else [p]
    if not paths:
        print("check-publish-runners: no workflow files found", file=sys.stderr)
        return 2
    bad, publishing = [], 0
    for p in paths:
        b, n = check_file(p)
        bad += b
        publishing += n
    for line in bad:
        print(line)
    if bad:
        print(f"check-publish-runners: {len(bad)} publishing job(s) can run on a self-hosted runner; publishing runs only "
              "on GitHub's hosted runners")
        return 1
    print(f"check-publish-runners: {len(paths)} workflow file(s), {publishing} publishing job(s), none on a self-hosted runner")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
