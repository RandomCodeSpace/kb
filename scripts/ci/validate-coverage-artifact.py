#!/usr/bin/env python3
"""Validate and safely extract the only artifact allowed across the secret boundary."""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import stat
import sys
import zipfile
import subprocess
from pathlib import Path, PurePosixPath

ARCHIVE_LIMIT = 10 * 1024 * 1024
ENTRY_LIMITS = {
    "lcov.info": 5 * 1024 * 1024,
    "go.out": 5 * 1024 * 1024,
    "manifest.json": 64 * 1024,
}
EXPECTED_ENTRIES = frozenset(ENTRY_LIMITS)
HEX_SHA = frozenset("0123456789abcdef")


def fail(message: str) -> None:
    raise ValueError(message)


def sha256(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def exact_keys(value: dict, keys: set[str], label: str) -> None:
    if set(value) != keys:
        fail(f"{label} keys differ: expected {sorted(keys)}, got {sorted(value)}")


def positive_int(value: object, label: str, allow_zero: bool = False) -> int:
    if not isinstance(value, int) or isinstance(value, bool) or value < (0 if allow_zero else 1) or value > 9_007_199_254_740_991:
        fail(f"{label} must be {'non-negative' if allow_zero else 'positive'} integer")
    return value


def validate_sha(value: object, label: str) -> str:
    if not isinstance(value, str) or len(value) != 40 or any(ch not in HEX_SHA for ch in value):
        fail(f"{label} must be a lowercase 40-character Git SHA")
    return value


def validate_manifest(manifest: dict, args: argparse.Namespace, reports: dict[str, bytes]) -> None:
    exact_keys(manifest, {"schema_version", "repository", "producer", "candidate", "reports", "tools"}, "manifest")
    if manifest["schema_version"] != 1:
        fail("unsupported manifest schema")
    if manifest["repository"] != args.repository:
        fail("manifest repository mismatch")

    producer = manifest["producer"]
    exact_keys(producer, {"workflow", "workflow_ref", "workflow_sha", "test_revision_sha", "run_id", "run_attempt", "job", "event"}, "producer")
    expected_producer = {
        "workflow": args.workflow,
        "run_id": args.run_id,
        "run_attempt": args.run_attempt,
        "job": "candidate_coverage",
        "event": args.event,
    }
    for key, expected in expected_producer.items():
        if producer[key] != expected:
            fail(f"producer {key} mismatch")
    if producer["workflow_ref"] != args.workflow_ref:
        fail("producer workflow_ref mismatch")
    if producer["workflow_sha"] != args.workflow_sha:
        fail("producer workflow_sha mismatch")
    if producer["test_revision_sha"] != args.test_revision_sha:
        fail("producer test_revision_sha mismatch")
    validate_sha(producer["workflow_sha"], "producer.workflow_sha")
    validate_sha(producer["test_revision_sha"], "producer.test_revision_sha")
    positive_int(producer["run_id"], "producer.run_id")
    positive_int(producer["run_attempt"], "producer.run_attempt")

    candidate = manifest["candidate"]
    exact_keys(candidate, {"repository", "sha", "tree", "ref", "pull_request", "base_sha", "base_ref", "security_base_sha", "maintenance"}, "candidate")
    expected_candidate = {
        "repository": args.candidate_repository,
        "sha": args.candidate_sha,
        "tree": args.candidate_tree,
        "ref": args.candidate_ref,
        "pull_request": args.pull_request,
        "base_ref": args.base_ref,
        "security_base_sha": args.security_base_sha,
        "maintenance": args.maintenance == "true",
    }
    for key, expected in expected_candidate.items():
        if candidate[key] != expected:
            fail(f"candidate {key} mismatch")
    validate_sha(candidate["sha"], "candidate.sha")
    validate_sha(candidate["tree"], "candidate.tree")
    validate_sha(candidate["base_sha"], "candidate.base_sha")
    validate_sha(candidate["security_base_sha"], "candidate.security_base_sha")
    if args.event == "pull_request" and candidate["base_sha"] != args.base_sha:
        fail("candidate base_sha mismatch")
    positive_int(candidate["pull_request"], "candidate.pull_request", allow_zero=True)
    if not isinstance(candidate["maintenance"], bool):
        fail("candidate.maintenance must be a boolean")
    if not isinstance(candidate["ref"], str) or not candidate["ref"] or any(ch in candidate["ref"] for ch in "\r\n\0"):
        fail("candidate ref is invalid")

    entries = manifest["reports"]
    exact_keys(entries, {"frontend", "go"}, "reports")
    for kind, archive_name, report_path in (
        ("frontend", "lcov.info", "coverage/lcov.info"),
        ("go", "go.out", "coverage/go.out"),
    ):
        report = entries[kind]
        exact_keys(report, {"path", "size", "sha256"}, f"reports.{kind}")
        if report["path"] != report_path:
            fail(f"reports.{kind}.path mismatch")
        if report["size"] != len(reports[archive_name]):
            fail(f"reports.{kind}.size mismatch")
        digest = sha256(reports[archive_name])
        if report["sha256"] != digest:
            fail(f"reports.{kind}.sha256 mismatch")

    tools = manifest["tools"]
    exact_keys(tools, {"node", "npm", "go", "go_module"}, "tools")
    if not all(isinstance(value, str) and value for value in tools.values()):
        fail("tool versions must be non-empty strings")


def safe_relative_source(value: str, label: str, repository_root: Path) -> None:
    path = PurePosixPath(value)
    if not value or path.is_absolute() or ".." in path.parts or "\\" in value or "://" in value or ":" in value or any(char in value for char in "\r\n\0"):
        fail(f"{label} contains an unsafe source path")
    if str(path) != value or len(path.parts) < 1:
        fail(f"{label} source path is not normalized")
    source = repository_root / value
    source_stat = source.lstat()
    if not stat.S_ISREG(source_stat.st_mode) or stat.S_ISLNK(source_stat.st_mode):
        fail(f"{label} source is not a regular non-symlink file: {value}")
    tracked = subprocess.run(
        ["git", "-C", str(repository_root), "ls-files", "--error-unmatch", "--", value],
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
        check=False,
    )
    if tracked.returncode != 0:
        fail(f"{label} source is not tracked: {value}")


def validate_report_sources(manifest: dict, reports: dict[str, bytes], repository_root: Path) -> None:
    try:
        lcov = reports["lcov.info"].decode("utf-8")
        go_report = reports["go.out"].decode("utf-8")
    except UnicodeDecodeError as error:
        fail(f"coverage report is not UTF-8: {error}")
    if "\r" in lcov or "\0" in lcov or "\r" in go_report or "\0" in go_report:
        fail("coverage report contains forbidden control bytes")
    lcov_sources = [line[3:] for line in lcov.splitlines() if line.startswith("SF:")]
    if not lcov_sources or not any(line.startswith("DA:") for line in lcov.splitlines()):
        fail("lcov.info has an invalid format")
    for source in lcov_sources:
        safe_relative_source(source, "lcov.info", repository_root)
    module = manifest["tools"]["go_module"]
    if not isinstance(module, str) or not module or any(char in module for char in "\r\n\0"):
        fail("tools.go_module is invalid")
    go_mod = (repository_root / "go.mod").read_text(encoding="utf-8")
    module_lines = [line.split(None, 1)[1] for line in go_mod.splitlines() if line.startswith("module ")]
    if module_lines != [module]:
        fail("tools.go_module does not match the candidate go.mod module")
    go_lines = go_report.splitlines()
    if not go_lines or go_lines[0] not in {"mode: set", "mode: count", "mode: atomic"} or len(go_lines) < 2:
        fail("go.out has an invalid format")
    import re
    pattern = re.compile(r"^(.+):[0-9]+\.[0-9]+,[0-9]+\.[0-9]+ [0-9]+ [0-9]+$")
    for line in go_lines[1:]:
        match = pattern.fullmatch(line)
        if not match or not match.group(1).startswith(f"{module}/"):
            fail("go.out has a malformed coverage entry")
        safe_relative_source(match.group(1)[len(module) + 1 :], "go.out", repository_root)


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument("--archive", required=True, type=Path)
    parser.add_argument("--output", required=True, type=Path)
    parser.add_argument("--repository", required=True)
    parser.add_argument("--workflow", required=True)
    parser.add_argument("--workflow-ref", required=True)
    parser.add_argument("--workflow-sha", required=True)
    parser.add_argument("--test-revision-sha", required=True)
    parser.add_argument("--run-id", required=True, type=int)
    parser.add_argument("--run-attempt", required=True, type=int)
    parser.add_argument("--event", required=True, choices=("pull_request", "push", "workflow_dispatch"))
    parser.add_argument("--candidate-repository", required=True)
    parser.add_argument("--candidate-sha", required=True)
    parser.add_argument("--candidate-tree", required=True)
    parser.add_argument("--candidate-ref", required=True)
    parser.add_argument("--pull-request", required=True, type=int)
    parser.add_argument("--base-sha", required=True)
    parser.add_argument("--security-base-sha", required=True)
    parser.add_argument("--base-ref", required=True)
    parser.add_argument("--maintenance", required=True, choices=("true", "false"))
    parser.add_argument("--repository-root", required=True, type=Path)
    parser.add_argument("--github-output", type=Path)
    return parser.parse_args()


def main() -> None:
    args = parse_args()
    archive_stat = args.archive.lstat()
    if not archive_stat.st_size or archive_stat.st_size > ARCHIVE_LIMIT or not stat.S_ISREG(archive_stat.st_mode):
        fail(f"artifact archive must be a regular file within 1..{ARCHIVE_LIMIT} bytes")

    with zipfile.ZipFile(args.archive) as archive:
        infos = archive.infolist()
        names = [info.filename for info in infos]
        if len(names) != len(set(names)):
            fail("artifact contains duplicate paths")
        if set(names) != EXPECTED_ENTRIES:
            fail(f"artifact entries differ: expected {sorted(EXPECTED_ENTRIES)}, got {sorted(names)}")
        reports: dict[str, bytes] = {}
        for info in infos:
            path = PurePosixPath(info.filename)
            unix_mode = info.external_attr >> 16
            if path.is_absolute() or ".." in path.parts or len(path.parts) != 1:
                fail(f"unsafe artifact path: {info.filename}")
            file_type = stat.S_IFMT(unix_mode)
            if info.is_dir() or stat.S_ISLNK(unix_mode) or (file_type and file_type != stat.S_IFREG):
                fail(f"artifact entry is not a regular non-symlink file: {info.filename}")
            if info.file_size < 1 or info.file_size > ENTRY_LIMITS[info.filename]:
                fail(f"artifact entry size out of bounds: {info.filename}")
            if info.compress_size and info.file_size / info.compress_size > 100:
                fail(f"artifact entry compression ratio too high: {info.filename}")
            reports[info.filename] = archive.read(info)

    if b"SF:" not in reports["lcov.info"] or b"DA:" not in reports["lcov.info"]:
        fail("lcov.info has an invalid format")
    if not reports["go.out"].startswith((b"mode: set\n", b"mode: count\n", b"mode: atomic\n")):
        fail("go.out has an invalid format")
    try:
        def unique_object(pairs):
            result = {}
            for key, value in pairs:
                if key in result:
                    fail(f"manifest.json contains duplicate key: {key}")
                result[key] = value
            return result
        manifest = json.loads(reports["manifest.json"], object_pairs_hook=unique_object)
    except (UnicodeDecodeError, json.JSONDecodeError) as error:
        fail(f"manifest.json is invalid JSON: {error}")
    if not isinstance(manifest, dict):
        fail("manifest root must be an object")
    validate_manifest(manifest, args, reports)
    empty_tree = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"
    manifest_base = manifest["candidate"]["base_sha"]
    if manifest_base != empty_tree:
        base_exists = subprocess.run(
            ["git", "-C", str(args.repository_root), "cat-file", "-e", f"{manifest_base}^{{commit}}"],
            stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, check=False,
        )
        if base_exists.returncode != 0:
            fail("candidate event base is not an available commit")
        if args.event != "pull_request":
            ancestor = subprocess.run(
                ["git", "-C", str(args.repository_root), "merge-base", "--is-ancestor", manifest_base, args.candidate_sha],
                stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, check=False,
            )
            if ancestor.returncode != 0:
                fail("candidate event base is not an ancestor of candidate SHA")
    validate_report_sources(manifest, reports, args.repository_root.resolve(strict=True))

    args.output.mkdir(mode=0o700, parents=True, exist_ok=False)
    for name in ("lcov.info", "go.out", "manifest.json"):
        target = args.output / name
        target.write_bytes(reports[name])
        target.chmod(0o600)

    if args.github_output:
        with args.github_output.open("a", encoding="utf-8") as output:
            output.write(f"lcov_sha256={sha256(reports['lcov.info'])}\n")
            output.write(f"go_sha256={sha256(reports['go.out'])}\n")


if __name__ == "__main__":
    try:
        main()
    except (OSError, ValueError, zipfile.BadZipFile) as error:
        print(f"validate-coverage-artifact: {error}", file=sys.stderr)
        sys.exit(1)
