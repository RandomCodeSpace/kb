#!/usr/bin/env python3

import json
import re
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
QUALITY = (ROOT / ".github/workflows/quality.yml").read_text()
SONAR = (ROOT / ".github/workflows/sonar-exact-revision.yml").read_text()
PROTECTED_SONAR = (ROOT / ".github/actions/protected-sonar/action.yml").read_text()
SONAR_PROPERTIES = (ROOT / "sonar-project.properties").read_text()
PACKAGE = json.loads((ROOT / "package.json").read_text())


class WorkflowStructureTest(unittest.TestCase):
    def test_privileged_permissions_are_read_only(self):
        permissions = re.search(r"^permissions:\n((?:  .+\n)+)", SONAR, re.MULTILINE)
        self.assertIsNotNone(permissions)
        self.assertEqual(permissions.group(1), "  actions: read\n  contents: read\n")
        self.assertNotIn("checks: write", SONAR)
        self.assertNotIn("statuses: write", SONAR)
        self.assertNotIn("pull_request_target", SONAR)

    def test_token_is_referenced_only_on_scan_step(self):
        self.assertEqual(SONAR.count("SONAR_TOKEN: ${{ secrets.SONAR_TOKEN }}"), 1)
        scan_step = SONAR.index("- name: Scan and verify exact Sonar revision")
        self.assertGreater(SONAR.index("SONAR_TOKEN: ${{ secrets.SONAR_TOKEN }}"), scan_step)

    def test_privileged_workflow_executes_only_trusted_helpers(self):
        self.assertNotRegex(SONAR, r"(?:sh|bash|node|python3) candidate/scripts/")
        self.assertIn("node trusted-main/scripts/ci/validate-workflow-run.cjs", SONAR)
        self.assertIn("python3 trusted-main/scripts/ci/validate-coverage-artifact.py", SONAR)
        self.assertIn("uses: ./trusted-main/.github/actions/protected-sonar", SONAR)

    def test_trigger_network_identifiers_are_passed_as_trusted_scalars(self):
        for name, expression in (
            ("TRIGGER_RUN_ID", "github.event.workflow_run.id"),
            ("TRIGGER_WORKFLOW_ID", "github.event.workflow_run.workflow_id"),
            ("TRIGGER_RUN_ATTEMPT", "github.event.workflow_run.run_attempt"),
            ("TRIGGER_HEAD_REPOSITORY_ID", "github.event.workflow_run.head_repository.id"),
            ("TRIGGER_HEAD_SHA", "github.event.workflow_run.head_sha"),
        ):
            self.assertIn(f"{name}: ${{{{ {expression} }}}}", SONAR)

    def test_control_plane_is_pinned_across_environment_approval(self):
        self.assertIn("ref: ${{ github.workflow_sha }}", SONAR)
        self.assertIn("control_plane_sha: ${{ steps.control-plane.outputs.sha }}", SONAR)
        self.assertIn("test_revision_sha: ${{ steps.run.outputs.workflow_sha }}", SONAR)
        self.assertIn("ref: ${{ needs.validate-candidate.outputs.control_plane_sha }}", SONAR)
        self.assertIn('test "$actual_sha" = "$EXPECTED_CONTROL_PLANE_SHA"', SONAR)
        self.assertNotIn("ref: ${{ github.event.repository.default_branch }}", SONAR)

    def test_maintenance_uses_only_pinned_sonar_configuration(self):
        self.assertIn("maintenance: ${{ steps.control-plane-change.outputs.maintenance }}", SONAR)
        self.assertIn('cp --remove-destination trusted-main/sonar-project.properties candidate/sonar-project.properties', SONAR)
        self.assertIn('test "$tracked_delta" = sonar-project.properties', SONAR)
        self.assertIn('--maintenance "$MAINTENANCE"', SONAR)
        self.assertNotIn("checks: write", SONAR)
        self.assertNotIn("statuses: write", SONAR)

    def test_candidate_coverage_has_no_secret_context(self):
        job = QUALITY.split("  candidate_coverage:\n", 1)[1]
        self.assertNotIn("secrets.", job)
        self.assertNotIn("github.token", job)
        self.assertNotIn("GITHUB_TOKEN", job)
        self.assertIn("repository: ${{ github.event.pull_request.head.repo.full_name || github.repository }}", job)
        self.assertIn("ref: ${{ github.event.pull_request.head.sha || github.sha }}", job)

    def test_branch_and_pr_control_plane_classification_match(self):
        branch = SONAR.split('elif [ "$ANALYSIS_MODE" = branch ]; then', 1)[1].split("else", 1)[0]
        self.assertIn('guard-control-plane.sh "$guard_base" "$CANDIDATE_SHA" --classify', branch)
        self.assertNotIn("echo 'maintenance=false'", branch)

    def test_fork_base_is_fetched_and_security_diff_uses_merge_base(self):
        self.assertIn('sh scripts/ci/fetch-verified-base.sh "$BASE_REPOSITORY" "$BASE_SHA"', QUALITY)
        self.assertEqual(SONAR.count('trusted-main/scripts/ci/fetch-verified-base.sh "$BASE_REPOSITORY" "$BASE_SHA"'), 2)
        self.assertIn('guard_base=$(git merge-base "$CANDIDATE_SHA" "$BASE_SHA")', SONAR)
        self.assertIn('merge-base "$CANDIDATE_SHA" "$BASE_SHA")" = "$SECURITY_BASE_SHA"', SONAR)

    def test_frontend_coverage_gate_and_candidate_report_are_separate(self):
        regression, candidate = QUALITY.split("  candidate_coverage:\n", 1)
        self.assertEqual(len(re.findall(r"^        run: npm test$", regression, re.MULTILINE)), 1)
        self.assertEqual(
            len(
                re.findall(
                    r"^        run: npm run test:coverage:report-only$",
                    candidate,
                    re.MULTILINE,
                )
            ),
            1,
        )
        self.assertEqual(len(re.findall(r"^        run: npm test$", candidate, re.MULTILINE)), 0)

        scripts = PACKAGE["scripts"]
        self.assertEqual(scripts["test"], "vitest run --coverage")
        self.assertEqual(
            scripts["test:coverage:report-only"],
            "vitest run --coverage --coverage.thresholds.lines=0 "
            "--coverage.thresholds.functions=0 --coverage.thresholds.branches=0 "
            "--coverage.thresholds.statements=0",
        )

    def test_sonar_scope_matches_enforced_application_coverage(self):
        values = {
            "sonar.projectVersion": "0.1.0-sonar.1",
            "sonar.exclusions": (
                "coverage/**,dist/**,node_modules/**,.omx/**,package-lock.json,"
                "tsconfig.tsbuildinfo"
            ),
            "sonar.test.inclusions": (
                "**/*_test.go,src/**/*.test.ts,src/**/*.test.tsx,src/test/**,"
                "scripts/**/*.test.sh,scripts/**/test-*.sh,scripts/**/test_*.py,"
                "scripts/**/test_*.cjs"
            ),
            "sonar.coverage.exclusions": ".github/**,scripts/**,vite.config.ts",
        }

        for key, value in values.items():
            self.assertIn(f"{key}={value}\n", SONAR_PROPERTIES)
            self.assertIn(f"-D{key}={value}\n", PROTECTED_SONAR)

        self.assertNotIn("sonar.exclusions=.github/**", SONAR_PROPERTIES)
        self.assertNotIn("-Dsonar.exclusions=.github/**", PROTECTED_SONAR)

    def test_candidate_tree_is_rejected_on_both_sides_of_approval(self):
        self.assertEqual(SONAR.count("trusted-main/scripts/ci/validate-candidate-tree.sh"), 2)
        self.assertIn("security_base_sha: ${{ steps.control-plane-change.outputs.security_base_sha }}", SONAR)


if __name__ == "__main__":
    unittest.main()
