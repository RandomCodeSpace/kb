#!/usr/bin/env python3

import re
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
QUALITY = (ROOT / ".github/workflows/quality.yml").read_text()
SONAR = (ROOT / ".github/workflows/sonar-exact-revision.yml").read_text()


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
        self.assertIn("ref: ${{ github.event.pull_request.head.sha || github.sha }}", job)

    def test_candidate_tree_is_rejected_on_both_sides_of_approval(self):
        self.assertEqual(SONAR.count("trusted-main/scripts/ci/validate-candidate-tree.sh"), 2)
        self.assertIn("security_base_sha: ${{ steps.control-plane-change.outputs.security_base_sha }}", SONAR)


if __name__ == "__main__":
    unittest.main()
