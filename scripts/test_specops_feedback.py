import importlib.util
import json
import os
import stat
import subprocess
import tempfile
from types import SimpleNamespace
import unittest
from pathlib import Path
from unittest import mock


MODULE_PATH = Path(__file__).with_name("specops-feedback.py")
spec = importlib.util.spec_from_file_location("specops_feedback", MODULE_PATH)
feedback = importlib.util.module_from_spec(spec)
spec.loader.exec_module(feedback)


class TargetAndEvidenceTests(unittest.TestCase):
    def test_m2_to_m4_are_rejected_before_preflight_or_workflow(self):
        for target in ("m2", "m3", "m4"):
            with self.subTest(target=target), \
                    mock.patch.object(feedback, "preflight", side_effect=AssertionError("must not inspect or mutate")), \
                    mock.patch.object(feedback, "run_walk", side_effect=AssertionError("must not run")):
                code = feedback.main(["walk", "--to", target, "--backend-repo", "/backend",
                                      "--database-repo", "/database", "--runtime-dir", "/runtime", "--cli", "/cli"])
                self.assertEqual(code, 20)

    def test_walk_preflight_failure_does_not_invoke_workflow(self):
        args = ["walk", "--to", "m1", "--backend-repo", "/backend", "--database-repo", "/database",
                "--runtime-dir", "/missing-private-runtime", "--cli", "/cli"]
        with mock.patch.object(feedback, "preflight", side_effect=feedback.FeedbackError("preflight rejected")), \
                mock.patch.object(feedback, "run_walk", side_effect=AssertionError("workflow must not run")):
            self.assertEqual(feedback.main(args), 1)

    def test_evidence_is_private_and_contains_only_compact_phase_data(self):
        with tempfile.TemporaryDirectory() as td:
            runtime = Path(td)
            runtime.chmod(0o700)
            record = {"schemaVersion": 1, "phases": {"m0": {"status": "passed", "seconds": 1.2}},
                      "revisions": {"backend": "abc"}}
            path = feedback.write_evidence(runtime, record)
            self.assertEqual(path.parent, runtime / "feedback-evidence")
            self.assertEqual(stat.S_IMODE(path.stat().st_mode), 0o600)
            self.assertEqual(stat.S_IMODE(path.parent.stat().st_mode), 0o700)
            self.assertEqual(json.loads(path.read_text()), record)

    def test_evidence_refuses_unsafe_runtime_directory(self):
        with tempfile.TemporaryDirectory() as td:
            runtime = Path(td)
            runtime.chmod(0o755)
            with self.assertRaises(feedback.FeedbackError):
                feedback.write_evidence(runtime, {"schemaVersion": 1})

    def test_child_environment_does_not_inherit_credentials(self):
        with mock.patch.dict(os.environ, {"PASSO_ACCESS_TOKEN": "secret-value", "OPENROUTER_API_KEY": "secret-value",
                                          "PATH": "/bin", "HOME": "/tmp/home"}, clear=True):
            env = feedback._safe_env()
        self.assertEqual(env["PATH"], "/bin")
        self.assertEqual(env["PYTHONDONTWRITEBYTECODE"], "1")
        self.assertNotIn("PASSO_ACCESS_TOKEN", env)
        self.assertNotIn("OPENROUTER_API_KEY", env)


class PreflightTests(unittest.TestCase):
    def test_docker_contract_requires_exact_project_service_and_database_port(self):
        self.assertTrue(feedback.validate_docker_summary("pyx-sandbox\tdb\t15432"))
        for bad in ("other\tdb\t15432", "pyx-sandbox\tapi\t15432", "pyx-sandbox\tdb\t5432",
                    "pyx-sandbox\tdb\t15432 15433", ""):
            with self.subTest(bad=bad), self.assertRaises(feedback.FeedbackError):
                feedback.validate_docker_summary(bad)

    def test_fixture_validation_requires_pinned_repository_source_and_acceptance_facts(self):
        with tempfile.TemporaryDirectory() as td:
            root = Path(td)
            fixture = root / "sandbox/specops/fixture/files/tinyGoApp.go"
            fixture.parent.mkdir(parents=True)
            fixture.write_bytes(b"wrong source")
            manifest = fixture.parents[1] / "manifest.json"
            manifest.write_text(json.dumps({"identity": feedback.FIXTURE_REPO,
                                            "commitSHA": feedback.FIXTURE_COMMIT}))
            realm = root / "sandbox/realm-export.json"
            realm.write_text("{}")
            acceptance = root / "acceptance.md"
            acceptance.write_text("FR-01 /healthz INV-01")
            with self.assertRaises(feedback.FeedbackError):
                feedback.validate_fixture_paths(root, acceptance)

    def test_global_timeout_is_bounded(self):
        self.assertEqual(feedback.validate_timeout(1800), 1800)
        for timeout in (0, 1801):
            with self.subTest(timeout=timeout), self.assertRaises(feedback.FeedbackError):
                feedback.validate_timeout(timeout)

    def test_launcher_status_must_prove_owned_ready_backend_on_expected_port(self):
        good = {"schemaVersion": 1, "status": "running", "port": 16080,
                "endpoint": "http://127.0.0.1:16080/readyz"}
        self.assertTrue(feedback.validate_launcher_status(good))
        for bad in ({**good, "status": "stopped"}, {**good, "port": 16081},
                    {**good, "endpoint": "http://127.0.0.1:16081/readyz"}, {"status": "running"}):
            with self.subTest(bad=bad), self.assertRaises(feedback.FeedbackError):
                feedback.validate_launcher_status(bad)

    def test_docker_queries_are_read_only_and_require_one_owned_container(self):
        with mock.patch.object(feedback, "_run_fixed", side_effect=["abc123", "pyx-sandbox\tdb\t15432 "]) as run:
            self.assertTrue(feedback.verify_docker_db())
        self.assertEqual(run.call_count, 2)
        self.assertEqual(run.call_args_list[0].args[0][0], "docker")
        self.assertEqual(run.call_args_list[1].args[0][1], "inspect")
        with mock.patch.object(feedback, "_run_fixed", return_value=""):
            with self.assertRaises(feedback.FeedbackError):
                feedback.verify_docker_db()


class ChildProcessTests(unittest.TestCase):
    def test_child_timeout_terminates_the_owned_process_group(self):
        proc = mock.Mock(pid=321, returncode=None)
        proc.communicate.side_effect = __import__("subprocess").TimeoutExpired(["python"], 1)
        with mock.patch.object(feedback.subprocess, "Popen", return_value=proc), \
                mock.patch.object(feedback, "_kill_child_group") as kill:
            with self.assertRaises(feedback.FeedbackError):
                feedback.run_child(["python", "script.py"], deadline=feedback.time.monotonic() + 1)
        kill.assert_called_once_with(proc)

    def test_nonzero_child_output_is_not_in_error(self):
        proc = mock.Mock(pid=123, returncode=7)
        proc.communicate.return_value = (b"token=not-for-log", None)
        with mock.patch.object(feedback.subprocess, "Popen", return_value=proc):
            with self.assertRaises(feedback.FeedbackError) as caught:
                feedback.run_child(["python", "script.py"], deadline=feedback.time.monotonic() + 5)
        self.assertNotIn("token", str(caught.exception))

    def test_m1_walk_supplies_explicit_fixtures_and_stable_project_names(self):
        with tempfile.TemporaryDirectory() as td:
            runtime = Path(td)
            runtime.chmod(0o700)
            backend = runtime / "backend"
            database = runtime / "database"
            source = backend / "sandbox/specops/fixture/files/tinyGoApp.go"
            realm = backend / "sandbox/realm-export.json"
            acceptance = runtime / "acceptance.md"
            for path in (source, realm, acceptance):
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_text("fixture")
            checks = {"cli": runtime / "passo", "fixtures": {"realm": realm, "source": source},
                      "revisions": {"backend": "a" * 40, "database": "b" * 40},
                      "checks": {"readyz": "http_200", "dockerDatabase": "ready", "nativeBackend": "running"},
                      "preflightSeconds": 0.1}
            args = SimpleNamespace(to="m1", max_runtime=300, runtime_dir=runtime)
            m0 = {"result": "passed", "projectId": "1", "stage": "BUILD.DEVELOP"}
            m1 = {"result": "passed", "projectId": "2", "runId": "run-1", "compilationRevision": 3}
            with mock.patch.object(feedback, "ensure_private_evidence_dir", return_value=runtime / "children"), \
                    mock.patch.object(feedback, "run_child", side_effect=[m0, m1]) as run_child:
                (runtime / "children").mkdir(mode=0o700)
                result = feedback.run_walk(args, checks)
            self.assertEqual(result["phases"]["m1"]["status"], "passed")
            first, second = [call.args[0] for call in run_child.call_args_list]
            for argv in (first, second):
                self.assertIn("--realm-fixture", argv)
                self.assertIn(str(realm), argv)
                self.assertIn("--evidence-dir", argv)
                self.assertIn("specops-local-feedback" if argv is first else "specops-local-fixture-walk", argv)
            self.assertIn("--source-fixture", second)
            self.assertIn(str(source), second)
            self.assertIn("--acceptance-fixture", second)


if __name__ == "__main__":
    unittest.main()
