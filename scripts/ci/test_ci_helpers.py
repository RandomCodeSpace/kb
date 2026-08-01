#!/usr/bin/env python3

import json
import os
import subprocess
import tempfile
import unittest
import shutil
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
DIFF = ROOT / "scripts/ci/check-event-diff.sh"
GUARD = ROOT / "scripts/ci/guard-control-plane.sh"
MANIFEST = ROOT / "scripts/ci/create-coverage-manifest.cjs"
TREE_GUARD = ROOT / "scripts/ci/validate-candidate-tree.sh"
EMPTY_TREE = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"


class DiffAndGuardTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.dir = Path(self.temp.name)
        self.bare = self.dir / "origin.git"
        self.repo = self.dir / "repo"
        subprocess.run(["git", "init", "-q", "--bare", self.bare], check=True)
        subprocess.run(["git", "init", "-q", "-b", "main", self.repo], check=True)
        subprocess.run(["git", "-C", self.repo, "config", "user.email", "ci@example.invalid"], check=True)
        subprocess.run(["git", "-C", self.repo, "config", "user.name", "CI"], check=True)
        subprocess.run(["git", "-C", self.repo, "remote", "add", "origin", self.bare], check=True)

    def tearDown(self):
        self.temp.cleanup()

    def commit(self, name, text):
        (self.repo / name).write_text(text)
        subprocess.run(["git", "-C", self.repo, "add", name], check=True)
        subprocess.run(["git", "-C", self.repo, "commit", "-qm", name], check=True)
        return subprocess.check_output(["git", "-C", self.repo, "rev-parse", "HEAD"], text=True).strip()

    def validate_tree(self):
        sha = subprocess.check_output(["git", "-C", self.repo, "rev-parse", "HEAD"], text=True).strip()
        tree = subprocess.check_output(["git", "-C", self.repo, "show", "-s", "--format=%T", "HEAD"], text=True).strip()
        return subprocess.run(
            ["bash", str(TREE_GUARD), str(self.repo), sha, tree],
            text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
        )

    def check(self, before, head, repo=None):
        repo = repo or self.repo
        event = self.dir / "event.json"
        event.write_text(json.dumps({"before": before}))
        summary = self.dir / "summary"
        output = self.dir / "output"
        env = os.environ | {
            "GITHUB_EVENT_NAME": "push", "GITHUB_EVENT_PATH": str(event), "GITHUB_SHA": head,
            "GITHUB_DEFAULT_BRANCH": "main", "GITHUB_STEP_SUMMARY": str(summary), "GITHUB_OUTPUT": str(output),
        }
        result = subprocess.run(["sh", str(DIFF)], cwd=repo, env=env, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        return result, output.read_text() if output.exists() else ""

    def test_root_push_uses_empty_tree(self):
        head = self.commit("root.txt", "root\n")
        subprocess.run(["git", "-C", self.repo, "push", "-q", "origin", "main"], check=True)
        result, output = self.check("0" * 40, head)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn(f"base={EMPTY_TREE}\n", output)

    def test_multi_commit_push_uses_event_before(self):
        before = self.commit("one.txt", "one\n")
        self.commit("two.txt", "two\n")
        head = self.commit("three.txt", "three\n")
        result, output = self.check(before, head)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn(f"base={before}\n", output)

    def test_force_push_compares_disconnected_tips(self):
        before = self.commit("old.txt", "old\n")
        subprocess.run(["git", "-C", self.repo, "push", "-q", "origin", "main"], check=True)
        subprocess.run(["git", "-C", self.repo, "checkout", "-q", "--orphan", "replacement"], check=True)
        subprocess.run(["git", "-C", self.repo, "rm", "-q", "-r", "--cached", "."], check=True)
        (self.repo / "old.txt").unlink()
        head = self.commit("new.txt", "new\n")
        subprocess.run(["git", "-C", self.repo, "push", "-q", "--force", "origin", "HEAD:main"], check=True)
        fresh = self.dir / "fresh"
        subprocess.run(["git", "clone", "-q", "--branch", "main", self.bare, fresh], check=True)
        result, output = self.check(before, head, fresh)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn(f"base={before}\n", output)

    def test_guard_blocks_codeowners(self):
        base = self.commit("safe.txt", "safe\n")
        (self.repo / ".github").mkdir()
        head = self.commit(".github/CODEOWNERS", "* @owner\n")
        result = subprocess.run(["bash", str(GUARD), base, head], cwd=self.repo, text=True, stderr=subprocess.PIPE)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("protected control-plane path changed", result.stderr)

    def test_guard_catches_protected_change_before_innocuous_tip(self):
        base = self.commit("base.txt", "base\n")
        (self.repo / ".github").mkdir()
        self.commit(".github/dependabot.yml", "version: 2\nupdates: []\n")
        head = self.commit("innocuous.txt", "later\n")
        result = subprocess.run(["bash", str(GUARD), base, head], cwd=self.repo, text=True, stderr=subprocess.PIPE)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn(".github/dependabot.yml", result.stderr)

    def test_same_repo_pr_can_classify_maintenance_without_trusting_it(self):
        base = self.commit("base.txt", "base\n")
        (self.repo / ".github").mkdir()
        head = self.commit(".github/CODEOWNERS", "* @owner\n")
        result = subprocess.run(
            ["bash", str(GUARD), base, head, "--classify"], cwd=self.repo,
            text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout.strip(), "maintenance=true")

    def test_ordinary_source_pr_classifies_non_maintenance(self):
        base = self.commit("base.txt", "base\n")
        (self.repo / "src").mkdir()
        head = self.commit("src/product.ts", "export const value = 1;\n")
        result = subprocess.run(
            ["bash", str(GUARD), base, head, "--classify"], cwd=self.repo,
            text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout.strip(), "maintenance=false")

    def test_mixed_control_plane_and_source_pr_fails_closed(self):
        base = self.commit("base.txt", "base\n")
        (self.repo / ".github/workflows").mkdir(parents=True)
        (self.repo / "src").mkdir()
        (self.repo / ".github/workflows/change.yml").write_text("name: changed\n")
        (self.repo / "src/product.ts").write_text("export const value = 1;\n")
        subprocess.run(
            ["git", "-C", self.repo, "add", ".github/workflows/change.yml", "src/product.ts"], check=True,
        )
        subprocess.run(["git", "-C", self.repo, "commit", "-qm", "mixed"], check=True)
        head = subprocess.check_output(["git", "-C", self.repo, "rev-parse", "HEAD"], text=True).strip()
        result = subprocess.run(
            ["bash", str(GUARD), base, head, "--classify"], cwd=self.repo,
            text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
        )
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("mixes protected control-plane and non-control-plane paths", result.stderr)

    def test_protected_to_unprotected_rename_cannot_hide_source_path(self):
        (self.repo / ".github/workflows").mkdir(parents=True)
        base = self.commit(".github/workflows/quality.yml", "name: protected\n")
        (self.repo / "src").mkdir()
        subprocess.run(["git", "-C", self.repo, "mv", ".github/workflows/quality.yml", "src/quality.yml"], check=True)
        subprocess.run(["git", "-C", self.repo, "commit", "-qm", "rename out"], check=True)
        head = subprocess.check_output(["git", "-C", self.repo, "rev-parse", "HEAD"], text=True).strip()
        for mode in ([], ["--classify"]):
            result = subprocess.run(["bash", str(GUARD), base, head, *mode], cwd=self.repo, text=True, stderr=subprocess.PIPE)
            self.assertNotEqual(result.returncode, 0)

    def test_unprotected_to_protected_rename_cannot_hide_destination(self):
        (self.repo / "src").mkdir()
        base = self.commit("src/quality.yml", "name: source\n")
        (self.repo / ".github/workflows").mkdir(parents=True)
        subprocess.run(["git", "-C", self.repo, "mv", "src/quality.yml", ".github/workflows/quality.yml"], check=True)
        subprocess.run(["git", "-C", self.repo, "commit", "-qm", "rename in"], check=True)
        head = subprocess.check_output(["git", "-C", self.repo, "rev-parse", "HEAD"], text=True).strip()
        for mode in ([], ["--classify"]):
            result = subprocess.run(["bash", str(GUARD), base, head, *mode], cwd=self.repo, text=True, stderr=subprocess.PIPE)
            self.assertNotEqual(result.returncode, 0)

    def test_relative_and_absolute_tracked_symlinks_are_rejected(self):
        self.commit("target.txt", "target\n")
        for link_target in ("target.txt", "/tmp/host-target"):
            link = self.repo / "tracked-link"
            if link.exists() or link.is_symlink():
                link.unlink()
            os.symlink(link_target, link)
            subprocess.run(["git", "-C", self.repo, "add", "tracked-link"], check=True)
            subprocess.run(["git", "-C", self.repo, "commit", "-qm", f"symlink {link_target}"], check=True)
            result = self.validate_tree()
            self.assertNotEqual(result.returncode, 0)
            self.assertIn("tracked symlink rejected", result.stderr)
            subprocess.run(["git", "-C", self.repo, "rm", "-q", "tracked-link"], check=True)
            subprocess.run(["git", "-C", self.repo, "commit", "-qm", "remove symlink"], check=True)

    def test_gitlink_is_rejected(self):
        commit_sha = self.commit("base.txt", "base\n")
        subprocess.run(
            ["git", "-C", self.repo, "update-index", "--add", "--cacheinfo", f"160000,{commit_sha},vendor/module"],
            check=True,
        )
        subprocess.run(["git", "-C", self.repo, "commit", "-qm", "gitlink"], check=True)
        result = self.validate_tree()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("gitlink/submodule rejected", result.stderr)

    def test_manifest_generation_binds_real_git_identity(self):
        (self.repo / "src").mkdir()
        (self.repo / "internal").mkdir()
        (self.repo / "coverage").mkdir()
        (self.repo / "scripts/ci").mkdir(parents=True)
        shutil.copy2(GUARD, self.repo / "scripts/ci/guard-control-plane.sh")
        (self.repo / "go.mod").write_text("module github.com/RandomCodeSpace/kb\n\ngo 1.24\n")
        (self.repo / ".gitignore").write_text("/coverage/\n")
        (self.repo / "src/a.ts").write_text("export const a = 1;\n")
        (self.repo / "internal/a.go").write_text("package internal\n")
        (self.repo / "coverage/lcov.info").write_text("TN:\nSF:src/a.ts\nDA:1,1\nend_of_record\n")
        (self.repo / "coverage/go.out").write_text("mode: atomic\ngithub.com/RandomCodeSpace/kb/internal/a.go:1.1,1.2 1 1\n")
        subprocess.run(["git", "-C", self.repo, "add", ".gitignore", "go.mod", "src/a.ts", "internal/a.go", "scripts/ci/guard-control-plane.sh"], check=True)
        subprocess.run(["git", "-C", self.repo, "commit", "-qm", "fixture"], check=True)
        sha = subprocess.check_output(["git", "-C", self.repo, "rev-parse", "HEAD"], text=True).strip()
        env = os.environ | {
            "CANDIDATE_SHA": sha, "CANDIDATE_REPOSITORY": "RandomCodeSpace/kb", "CANDIDATE_REF": "feature",
            "PULL_REQUEST_NUMBER": "7", "BASE_SHA": sha, "BASE_REF": "main",
            "GITHUB_REPOSITORY": "RandomCodeSpace/kb", "GITHUB_WORKFLOW": "Regression and candidate coverage",
            "GITHUB_WORKFLOW_REF": "RandomCodeSpace/kb/.github/workflows/quality.yml@refs/pull/7/merge",
            "GITHUB_WORKFLOW_SHA": sha, "GITHUB_RUN_ID": "10", "GITHUB_RUN_ATTEMPT": "1",
            "GITHUB_JOB": "candidate_coverage", "GITHUB_EVENT_NAME": "pull_request",
        }
        result = subprocess.run(["node", str(MANIFEST)], cwd=self.repo, env=env, text=True, stderr=subprocess.PIPE)
        self.assertEqual(result.returncode, 0, result.stderr)
        manifest = json.loads((self.repo / "coverage/manifest.json").read_text())
        self.assertEqual(manifest["candidate"]["sha"], sha)
        self.assertEqual(manifest["producer"]["workflow_sha"], sha)
        self.assertFalse(manifest["candidate"]["maintenance"])

        (self.repo / "coverage/manifest.json").unlink()
        (self.repo / "src/a.ts").write_text("export const a = 2;\n")
        dirty = subprocess.run(["node", str(MANIFEST)], cwd=self.repo, env=env, text=True, stderr=subprocess.PIPE)
        self.assertNotEqual(dirty.returncode, 0)
        self.assertIn("changed tracked or non-ignored", dirty.stderr)
        subprocess.run(["git", "-C", self.repo, "checkout", "--", "src/a.ts"], check=True)

        (self.repo / ".github").mkdir()
        (self.repo / ".github/CODEOWNERS").write_text("* @owner\n")
        subprocess.run(["git", "-C", self.repo, "add", ".github/CODEOWNERS"], check=True)
        subprocess.run(["git", "-C", self.repo, "commit", "-qm", "maintenance"], check=True)
        maintenance_sha = subprocess.check_output(["git", "-C", self.repo, "rev-parse", "HEAD"], text=True).strip()
        maintenance_env = env | {
            "CANDIDATE_SHA": maintenance_sha,
            "BASE_SHA": sha,
            "GITHUB_WORKFLOW_SHA": maintenance_sha,
        }
        result = subprocess.run(["node", str(MANIFEST)], cwd=self.repo, env=maintenance_env, text=True, stderr=subprocess.PIPE)
        self.assertEqual(result.returncode, 0, result.stderr)
        manifest = json.loads((self.repo / "coverage/manifest.json").read_text())
        self.assertTrue(manifest["candidate"]["maintenance"])

    def test_push_and_dispatch_manifest_preserve_event_base(self):
        (self.repo / "src").mkdir()
        (self.repo / "internal").mkdir()
        (self.repo / "coverage").mkdir()
        (self.repo / ".gitignore").write_text("/coverage/\n")
        (self.repo / "go.mod").write_text("module github.com/RandomCodeSpace/kb\n\ngo 1.24\n")
        (self.repo / "src/a.ts").write_text("export const a = 1;\n")
        (self.repo / "internal/a.go").write_text("package internal\n")
        subprocess.run(["git", "-C", self.repo, "add", ".gitignore", "go.mod", "src/a.ts", "internal/a.go"], check=True)
        subprocess.run(["git", "-C", self.repo, "commit", "-qm", "base"], check=True)
        event_base = subprocess.check_output(["git", "-C", self.repo, "rev-parse", "HEAD"], text=True).strip()
        subprocess.run(["git", "-C", self.repo, "push", "-q", "origin", "HEAD:main"], check=True)
        self.commit("second.txt", "second\n")
        head = self.commit("third.txt", "third\n")
        (self.repo / "coverage/lcov.info").write_text("TN:\nSF:src/a.ts\nDA:1,1\nend_of_record\n")
        (self.repo / "coverage/go.out").write_text("mode: atomic\ngithub.com/RandomCodeSpace/kb/internal/a.go:1.1,1.2 1 1\n")
        env = os.environ | {
            "CANDIDATE_SHA": head, "CANDIDATE_REPOSITORY": "RandomCodeSpace/kb", "CANDIDATE_REF": "feature",
            "PULL_REQUEST_NUMBER": "0", "BASE_SHA": event_base, "BASE_REF": "main",
            "GITHUB_REPOSITORY": "RandomCodeSpace/kb", "GITHUB_WORKFLOW": "Regression and candidate coverage",
            "GITHUB_WORKFLOW_REF": "RandomCodeSpace/kb/.github/workflows/quality.yml@refs/heads/feature",
            "GITHUB_WORKFLOW_SHA": head, "GITHUB_RUN_ID": "10", "GITHUB_RUN_ATTEMPT": "1",
            "GITHUB_JOB": "candidate_coverage", "GITHUB_EVENT_NAME": "push",
        }
        result = subprocess.run(["node", str(MANIFEST)], cwd=self.repo, env=env, text=True, stderr=subprocess.PIPE)
        self.assertEqual(result.returncode, 0, result.stderr)
        manifest = json.loads((self.repo / "coverage/manifest.json").read_text())
        self.assertEqual(manifest["candidate"]["base_sha"], event_base)
        self.assertNotEqual(manifest["candidate"]["base_sha"], subprocess.check_output(["git", "-C", self.repo, "rev-parse", "HEAD^"], text=True).strip())
        self.assertEqual(manifest["candidate"]["security_base_sha"], event_base)

        for event_name, event_sha in (("push", EMPTY_TREE), ("workflow_dispatch", event_base)):
            (self.repo / "coverage/manifest.json").unlink()
            event_env = env | {"GITHUB_EVENT_NAME": event_name, "BASE_SHA": event_sha}
            result = subprocess.run(["node", str(MANIFEST)], cwd=self.repo, env=event_env, text=True, stderr=subprocess.PIPE)
            self.assertEqual(result.returncode, 0, result.stderr)
            manifest = json.loads((self.repo / "coverage/manifest.json").read_text())
            self.assertEqual(manifest["candidate"]["base_sha"], event_sha)


if __name__ == "__main__":
    unittest.main()
