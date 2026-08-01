#!/usr/bin/env python3

import hashlib
import json
import os
import stat
import subprocess
import tempfile
import unittest
import zipfile
from pathlib import Path

SCRIPT = Path(__file__).with_name("validate-coverage-artifact.py")
SHA = "1" * 40
TREE = "2" * 40
BASE = "3" * 40
WORKFLOW_SHA = "4" * 40


class ArtifactValidationTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.root = Path(self.temp.name)
        (self.root / "src").mkdir()
        (self.root / "internal").mkdir()
        (self.root / "src/a.ts").write_text("export const a = 1;\n")
        (self.root / "-tracked.ts").write_text("export const tracked = true;\n")
        (self.root / "internal/a.go").write_text("package internal\n")
        (self.root / "go.mod").write_text("module github.com/RandomCodeSpace/kb\n\ngo 1.24\n")
        subprocess.run(["git", "init", "-q", self.root], check=True)
        subprocess.run(["git", "-C", self.root, "config", "user.email", "ci@example.invalid"], check=True)
        subprocess.run(["git", "-C", self.root, "config", "user.name", "CI"], check=True)
        subprocess.run(["git", "-C", self.root, "add", "--", "src/a.ts", "-tracked.ts", "internal/a.go", "go.mod"], check=True)
        subprocess.run(["git", "-C", self.root, "commit", "-qm", "fixture"], check=True)
        self.base = subprocess.check_output(["git", "-C", self.root, "rev-parse", "HEAD"], text=True).strip()
        self.lcov = b"TN:\nSF:src/a.ts\nDA:1,1\nend_of_record\n"
        self.go = b"mode: atomic\ngithub.com/RandomCodeSpace/kb/internal/a.go:1.1,1.2 1 1\n"

    def tearDown(self):
        self.temp.cleanup()

    def manifest(self):
        return {
            "schema_version": 1,
            "repository": "RandomCodeSpace/kb",
            "producer": {
                "workflow": "Regression and candidate coverage",
                "workflow_ref": "RandomCodeSpace/kb/.github/workflows/quality.yml@refs/pull/7/merge",
                "workflow_sha": WORKFLOW_SHA,
                "test_revision_sha": WORKFLOW_SHA,
                "run_id": 10,
                "run_attempt": 1,
                "job": "candidate_coverage",
                "event": "pull_request",
            },
            "candidate": {
                "repository": "RandomCodeSpace/kb", "sha": SHA, "tree": TREE,
                "ref": "feature", "pull_request": 7, "base_sha": self.base, "base_ref": "main",
                "maintenance": False,
                "security_base_sha": self.base,
            },
            "reports": {
                "frontend": {"path": "coverage/lcov.info", "size": len(self.lcov), "sha256": hashlib.sha256(self.lcov).hexdigest()},
                "go": {"path": "coverage/go.out", "size": len(self.go), "sha256": hashlib.sha256(self.go).hexdigest()},
            },
            "tools": {"node": "v24", "npm": "11", "go": "go1.26", "go_module": "github.com/RandomCodeSpace/kb"},
        }

    def archive(self, *, lcov=None, go=None, manifest_bytes=None, symlink=False):
        path = self.root / f"artifact-{os.urandom(4).hex()}.zip"
        manifest = self.manifest()
        lcov = self.lcov if lcov is None else lcov
        go = self.go if go is None else go
        if lcov != self.lcov:
            manifest["reports"]["frontend"] = {"path": "coverage/lcov.info", "size": len(lcov), "sha256": hashlib.sha256(lcov).hexdigest()}
        if go != self.go:
            manifest["reports"]["go"] = {"path": "coverage/go.out", "size": len(go), "sha256": hashlib.sha256(go).hexdigest()}
        manifest_bytes = json.dumps(manifest).encode() if manifest_bytes is None else manifest_bytes
        with zipfile.ZipFile(path, "w", zipfile.ZIP_DEFLATED) as archive:
            info = zipfile.ZipInfo("lcov.info")
            info.compress_type = zipfile.ZIP_DEFLATED
            info.external_attr = ((stat.S_IFLNK if symlink else stat.S_IFREG) | 0o600) << 16
            archive.writestr(info, lcov)
            archive.writestr("go.out", go)
            archive.writestr("manifest.json", manifest_bytes)
        return path

    def run_validator(self, archive, output=None, *, candidate_sha=SHA, base_sha=None, event="pull_request", pull_request=7):
        output = output or self.root / f"out-{os.urandom(4).hex()}"
        base_sha = self.base if base_sha is None else base_sha
        args = [
            "python3", str(SCRIPT), "--archive", str(archive), "--output", str(output),
            "--repository", "RandomCodeSpace/kb", "--workflow", "Regression and candidate coverage",
            "--workflow-ref", "RandomCodeSpace/kb/.github/workflows/quality.yml@refs/pull/7/merge", "--workflow-sha", WORKFLOW_SHA,
            "--test-revision-sha", WORKFLOW_SHA,
            "--run-id", "10", "--run-attempt", "1", "--event", event,
            "--candidate-repository", "RandomCodeSpace/kb", f"--candidate-sha={candidate_sha}",
            "--candidate-tree", TREE, "--candidate-ref", "feature", "--pull-request", str(pull_request),
            f"--base-sha={base_sha}", "--security-base-sha", self.base, "--base-ref", "main", "--repository-root", str(self.root),
            "--maintenance", "false",
        ]
        return subprocess.run(args, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE)

    def test_accepts_exact_bundle(self):
        self.assertEqual(self.run_validator(self.archive()).returncode, 0)

    def test_accepts_tracked_source_with_option_like_name(self):
        lcov = b"TN:\nSF:-tracked.ts\nDA:1,1\nend_of_record\n"
        self.assertEqual(self.run_validator(self.archive(lcov=lcov)).returncode, 0)

    def test_accepts_valid_push_revision_boundary(self):
        manifest = self.manifest()
        manifest["producer"]["event"] = "push"
        manifest["candidate"]["sha"] = self.base
        manifest["candidate"]["pull_request"] = 0
        result = self.run_validator(
            self.archive(manifest_bytes=json.dumps(manifest).encode()),
            candidate_sha=self.base,
            event="push",
            pull_request=0,
        )
        self.assertEqual(result.returncode, 0, result.stderr)

    def test_rejects_option_like_traversal_and_control_git_revisions(self):
        for field, value in (
            ("sha", "--help"),
            ("base_sha", "../HEAD"),
            ("sha", "1" * 39 + "\n"),
        ):
            with self.subTest(field=field, value=value):
                manifest = self.manifest()
                manifest["candidate"][field] = value
                result = self.run_validator(
                    self.archive(manifest_bytes=json.dumps(manifest).encode()),
                    candidate_sha=value if field == "sha" else SHA,
                    base_sha=value if field == "base_sha" else self.base,
                )
                self.assertNotEqual(result.returncode, 0)
                self.assertIn("must be a lowercase 40-character Git SHA", result.stderr)

    def test_rejects_traversal_and_url_sources(self):
        for source in ("../src/a.ts", "/src/a.ts", "https://evil.invalid/a.ts", "src//a.ts"):
            with self.subTest(source=source):
                result = self.run_validator(self.archive(lcov=f"TN:\nSF:{source}\nDA:1,1\nend_of_record\n".encode()))
                self.assertNotEqual(result.returncode, 0)

    def test_rejects_untracked_source(self):
        (self.root / "src/untracked.ts").write_text("x")
        result = self.run_validator(self.archive(lcov=b"TN:\nSF:src/untracked.ts\nDA:1,1\nend_of_record\n"))
        self.assertNotEqual(result.returncode, 0)

    def test_rejects_duplicate_json_key(self):
        raw = json.dumps(self.manifest()).encode().replace(b'"schema_version": 1', b'"schema_version": 1, "schema_version": 1', 1)
        self.assertNotEqual(self.run_validator(self.archive(manifest_bytes=raw)).returncode, 0)

    def test_rejects_symlink_entry(self):
        self.assertNotEqual(self.run_validator(self.archive(symlink=True)).returncode, 0)

    def test_rejects_go_traversal(self):
        go = b"mode: atomic\ngithub.com/RandomCodeSpace/kb/../go.mod:1.1,1.2 1 1\n"
        self.assertNotEqual(self.run_validator(self.archive(go=go)).returncode, 0)

    def test_rejects_missing_extra_and_duplicate_entries(self):
        cases = []
        missing = self.root / "missing.zip"
        with zipfile.ZipFile(missing, "w") as archive:
            archive.writestr("lcov.info", self.lcov)
            archive.writestr("go.out", self.go)
        cases.append(missing)
        extra = self.archive()
        with zipfile.ZipFile(extra, "a") as archive:
            archive.writestr("extra.txt", b"no")
        cases.append(extra)
        duplicate = self.archive()
        with self.assertWarns(UserWarning):
            with zipfile.ZipFile(duplicate, "a") as archive:
                archive.writestr("go.out", self.go)
        cases.append(duplicate)
        for archive in cases:
            with self.subTest(archive=archive.name):
                self.assertNotEqual(self.run_validator(archive).returncode, 0)

    def test_rejects_empty_oversize_and_high_ratio_entries(self):
        for lcov in (b"", b"x" * (5 * 1024 * 1024 + 1), b"TN:\nSF:src/a.ts\nDA:1,1\n" + b"A" * 1024 * 1024):
            with self.subTest(size=len(lcov)):
                self.assertNotEqual(self.run_validator(self.archive(lcov=lcov)).returncode, 0)

    def test_rejects_wrong_hash_size_and_metadata(self):
        for mutation in ("hash", "size", "repository", "ref", "maintenance"):
            manifest = self.manifest()
            if mutation == "hash": manifest["reports"]["frontend"]["sha256"] = "0" * 64
            if mutation == "size": manifest["reports"]["frontend"]["size"] += 1
            if mutation == "repository": manifest["repository"] = "evil/repo"
            if mutation == "ref": manifest["candidate"]["ref"] = "other"
            if mutation == "maintenance": manifest["candidate"]["maintenance"] = True
            with self.subTest(mutation=mutation):
                self.assertNotEqual(self.run_validator(self.archive(manifest_bytes=json.dumps(manifest).encode())).returncode, 0)

    def test_rejects_control_bytes_and_invalid_utf8(self):
        for lcov in (b"TN:\r\nSF:src/a.ts\r\nDA:1,1\r\n", b"TN:\nSF:src/a.ts\0\nDA:1,1\n", b"TN:\nSF:\xff\nDA:1,1\n"):
            with self.subTest(lcov=lcov):
                self.assertNotEqual(self.run_validator(self.archive(lcov=lcov)).returncode, 0)

    def test_rejects_existing_output_directory(self):
        output = self.root / "already-exists"
        output.mkdir()
        self.assertNotEqual(self.run_validator(self.archive(), output).returncode, 0)


if __name__ == "__main__":
    unittest.main()
