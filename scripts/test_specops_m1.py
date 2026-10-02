import importlib.util
import hashlib
import json
import tempfile
import unittest
from pathlib import Path


MODULE_PATH = Path(__file__).with_name("specops-m1.py")
spec = importlib.util.spec_from_file_location("specops_m1", MODULE_PATH)
m1 = importlib.util.module_from_spec(spec)
spec.loader.exec_module(m1)


class EnvelopeValidationTests(unittest.TestCase):
    def test_rejects_wrong_project_or_profile(self):
        good = {"schemaVersion": 1, "profile": "sandbox", "projectId": 7, "data": {"status": "succeeded"}}
        self.assertEqual(m1.validate_envelope(good, 7)["status"], "succeeded")
        for bad in (
            {**good, "projectId": 8},
            {**good, "profile": "production"},
            {**good, "schemaVersion": 2},
            {**good, "data": []},
        ):
            with self.subTest(bad=bad), self.assertRaises(ValueError):
                m1.validate_envelope(bad, 7)

    def test_run_output_must_complete_requested_cli_stage(self):
        good = {"schemaVersion": 1, "status": "completed", "stage": "connect", "results": []}
        self.assertIs(m1.validate_run_output(good, 7, "connect"), good)
        with self.assertRaises(ValueError):
            m1.validate_run_output({**good, "status": "failed"}, 7, "connect")


class PollingTests(unittest.TestCase):
    def test_polling_requires_succeeded_and_valid_provenance(self):
        report = {"runId": "run-1", "status": "succeeded", "report": {"sourceSnapshot": {"repositories": [{
            "identity": m1.FIXTURE_REPO, "commitSha": m1.FIXTURE_COMMIT,
        }]}}}
        self.assertEqual(m1.validate_succeeded_report(report)["runId"], "run-1")
        for bad in (
            {**report, "status": "running"},
            {**report, "report": {}},
            {**report, "runId": ""},
            {"runId": "run-1", "status": "succeeded", "report": {"sourceSnapshot": {"repositories": [{"identity": m1.FIXTURE_REPO, "commitSha": "other"}]}}},
        ):
            with self.subTest(bad=bad), self.assertRaises(ValueError):
                m1.validate_succeeded_report(bad)

    def test_scope_must_include_fixture_functional_and_invariant_rows(self):
        good = {"scope_contract": {"candidateId": "comp-1", "result": {
            "requirements": [{"id": "FR-01"}], "invariants": [{"id": "INV-01"}],
        }}}
        self.assertEqual(m1.validate_nonempty_scope(good), "comp-1")
        with self.assertRaises(ValueError):
            m1.validate_nonempty_scope({"scope_contract": {"candidateId": "comp-1", "result": {"requirements": [], "invariants": []}}})


class FixtureSelectionTests(unittest.TestCase):
    def test_existing_fixture_requires_unique_filename_and_size_match(self):
        rows = [{"id": "doc-1", "fileName": "specops-acceptance.md", "sizeBytes": 10, "status": "READY"}]
        self.assertEqual(m1.unique_fixture_document(rows, "specops-acceptance.md", 10)["id"], "doc-1")
        for bad in (rows + rows, [{**rows[0], "sizeBytes": 11}], [{**rows[0], "status": "FAILED"}]):
            with self.subTest(bad=bad), self.assertRaises(ValueError):
                m1.unique_fixture_document(bad, "specops-acceptance.md", 10)

    def test_acceptance_fixture_label_is_separate_from_repository_source_provenance(self):
        with tempfile.TemporaryDirectory() as td:
            fixture = Path(td) / "acceptance.md"
            fixture.write_text("owner accepted behavior")
            digest = hashlib.sha256(fixture.read_bytes()).hexdigest()
            size = fixture.stat().st_size
            harness = m1.Harness.__new__(m1.Harness)
            harness.project_id = "1"
            harness.outcomes = {"fixtureSourceLabel": "fixture_source_not_repository_proof"}
            filename = f"specops-acceptance-{digest[:16]}.md"
            harness.public_json = lambda method, url, **kwargs: {
                "documents": [{"id": "doc-1", "fileName": filename, "sizeBytes": size, "status": "READY"}]}
            self.assertEqual(harness.ensure_fixture_document(fixture), "doc-1")
            self.assertEqual(harness.outcomes["fixtureSourceLabel"], "fixture_source_not_repository_proof")
            self.assertEqual(harness.outcomes["fixtureDocumentSourceLabel"],
                             "user_acceptance_fixture_not_repository_proof")
            self.assertEqual(harness.outcomes["fixtureContentSha256"], digest)

    def test_compile_body_leaves_idempotency_key_to_cli(self):
        self.assertEqual(m1.compile_input("doc-1"), {"documentIds": ["doc-1"]})
        with self.assertRaises(ValueError):
            m1.compile_input("")

    def test_finds_workspace_root_above_linked_worktree(self):
        with tempfile.TemporaryDirectory() as td:
            root = Path(td)
            sandbox = root / ".worktrees/specops-backend/sandbox"
            fixture = sandbox / "specops/fixture/files/tinyGoApp.go"
            fixture.parent.mkdir(parents=True)
            fixture.write_text("package main")
            (sandbox / "realm-export.json").write_text("{}")
            cli_script = root / ".worktrees/specops-m1-harness/scripts/specops-m1.py"
            cli_script.parent.mkdir(parents=True)
            cli_script.touch()
            self.assertEqual(m1.find_fixture_backend(cli_script), sandbox.resolve())


if __name__ == "__main__":
    unittest.main()
