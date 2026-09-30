import importlib.util
import json
import os
import stat
import subprocess
import tempfile
from types import SimpleNamespace
import unittest
from pathlib import Path
import uuid
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
    def test_docker_context_must_resolve_to_local_unix_socket(self):
        with mock.patch.dict(os.environ, {"DOCKER_HOST": "tcp://remote.example:2376"}, clear=False):
            with self.assertRaises(feedback.FeedbackError):
                feedback.verify_local_docker_context()
        with mock.patch.object(feedback, "_run_fixed", side_effect=["desktop-linux", "unix:///var/run/docker.sock"]):
            self.assertTrue(feedback.verify_local_docker_context())
        with mock.patch.object(feedback, "_run_fixed", side_effect=["remote", "ssh://host"]):
            with self.assertRaises(feedback.FeedbackError):
                feedback.verify_local_docker_context()

    def test_docker_contract_requires_loopback_binding_and_exact_internal_network(self):
        ports = [{"HostIp": "127.0.0.1", "HostPort": "15432"}]
        networks = {feedback.SAFE_NETWORK: {}}
        self.assertTrue(feedback.validate_docker_summary("pyx-sandbox", "db", ports, networks))
        bad_rows = (
            ("other", "db", ports, networks),
            ("pyx-sandbox", "api", ports, networks),
            ("pyx-sandbox", "db", [{"HostIp": "0.0.0.0", "HostPort": "15432"}], networks),
            ("pyx-sandbox", "db", [{"HostIp": "::", "HostPort": "15432"}], networks),
            ("pyx-sandbox", "db", ports + [{"HostIp": "127.0.0.1", "HostPort": "15433"}], networks),
            ("pyx-sandbox", "db", ports, {"pyx-sandbox_default": {}}),
            ("pyx-sandbox", "db", ports, {feedback.SAFE_NETWORK: {}, "external": {}}),
        )
        for row in bad_rows:
            with self.subTest(row=row), self.assertRaises(feedback.FeedbackError):
                feedback.validate_docker_summary(*row)

    def test_docker_network_must_be_inspected_internal(self):
        with mock.patch.object(feedback, "_run_fixed", return_value="true") as run:
            self.assertTrue(feedback.verify_docker_network())
        self.assertIn("network", run.call_args.args[0])
        with mock.patch.object(feedback, "_run_fixed", return_value="false"):
            with self.assertRaises(feedback.FeedbackError):
                feedback.verify_docker_network()

    def test_sso_forwarder_must_be_loopback_only_on_approved_network(self):
        with mock.patch.object(feedback, "verify_docker_network", return_value=True):
            self.assertTrue(feedback.validate_docker_forwarder_summary(
                "pyx-sandbox", "expose-sso", [{"HostIp": "127.0.0.1", "HostPort": "18081"}],
                {feedback.SAFE_NETWORK: {}}))
            invalid = [
                ("pyx-sandbox", "expose-sso", [{"HostIp": "0.0.0.0", "HostPort": "18081"}],
                 {feedback.SAFE_NETWORK: {}}),
                ("pyx-sandbox", "expose-sso", [{"HostIp": "127.0.0.1", "HostPort": "18081"}],
                 {"pyx-sandbox_default": {}}),
                ("other", "expose-sso", [{"HostIp": "127.0.0.1", "HostPort": "18081"}],
                 {feedback.SAFE_NETWORK: {}}),
            ]
            for args in invalid:
                with self.subTest(args=args), self.assertRaises(feedback.FeedbackError):
                    feedback.validate_docker_forwarder_summary(*args)

    def test_sso_must_use_private_stable_realm_internal_network_and_named_h2_volume(self):
        with tempfile.TemporaryDirectory() as td:
            realm = Path(td) / "realm-export.safe.json"
            volume = "pyx-sandbox-specops-sso-a1b2c3d4"
            valid_mounts = [
                {"Type": "bind", "Source": str(realm), "Destination": feedback.SSO_REALM_EXPORT_TARGET},
                {"Type": "volume", "Name": volume,
                 "Source": "/var/lib/docker/volumes/specops/_data", "Destination": feedback.SSO_H2_TARGET},
            ]
            self.assertTrue(feedback.validate_docker_sso_summary(
                "pyx-sandbox", "sso", {"8080/tcp": None}, {feedback.SAFE_NETWORK: {}}, valid_mounts, realm,
                volume))
            invalid = [
                ("pyx-sandbox", "sso", {"8080/tcp": [{"HostIp": "0.0.0.0"}]},
                 {feedback.SAFE_NETWORK: {}}, valid_mounts),
                ("pyx-sandbox", "sso", {}, {"pyx-sandbox_default": {}}, valid_mounts),
                ("pyx-sandbox", "sso", {}, {feedback.SAFE_NETWORK: {}}, [valid_mounts[1]]),
                ("pyx-sandbox", "sso", {}, {feedback.SAFE_NETWORK: {}}, [valid_mounts[0],
                 {**valid_mounts[1], "Name": "foreign-volume"}]),
            ]
            for project, service, ports, networks, mounts in invalid:
                with self.subTest(mounts=mounts), self.assertRaises(feedback.FeedbackError):
                    feedback.validate_docker_sso_summary(project, service, ports, networks, mounts, realm, volume)

    def test_sso_data_volume_name_is_pinned_to_backend_worktree(self):
        with tempfile.TemporaryDirectory() as td:
            root = Path(td)
            subprocess.run(["git", "init", "--quiet", str(root)], check=True)
            digest = __import__("hashlib").sha1(str(root.resolve()).encode()).hexdigest()[:8]
            self.assertEqual(feedback.expected_sso_volume(root), f"pyx-sandbox-specops-sso-{digest}")

    def test_safe_realm_requires_deterministic_fixture_user_ids_and_exact_contents(self):
        with tempfile.TemporaryDirectory() as td:
            root = Path(td)
            backend = root / "backend"
            runtime = root / "runtime"
            (backend / "sandbox").mkdir(parents=True)
            runtime.mkdir(mode=0o700)
            source = {"realm": "passobuild", "users": [{"username": "a@example.test"}]}
            (backend / "sandbox/realm-export.json").write_text(json.dumps(source))
            user = source["users"][0]
            user["id"] = str(uuid.uuid5(feedback.SAFE_REALM_USER_NAMESPACE, "passobuild/a@example.test"))
            safe_file = runtime / "realm-export.safe.json"
            safe_file.write_text(json.dumps(source))
            safe_file.chmod(0o600)
            self.assertEqual(feedback.validate_safe_realm_fixture(backend, runtime), safe_file)
            source["users"][0]["id"] = str(uuid.uuid4())
            safe_file.write_text(json.dumps(source))
            with self.assertRaises(feedback.FeedbackError):
                feedback.validate_safe_realm_fixture(backend, runtime)

    def test_launcher_guard_requires_matching_private_policy_hash(self):
        with tempfile.TemporaryDirectory() as td:
            profile = b'(version 1)\n(deny default)\n'
            runtime = Path(td)
            metadata = {"schemaVersion": 1, "status": "running", "port": 16080,
                        "runtimeDir": str(runtime),
                        "guard": {"name": feedback.GUARD_NAME, "version": feedback.GUARD_VERSION,
                                  "profileSha256": __import__("hashlib").sha256(profile).hexdigest(),
                                  "selfTest": "passed"}}
            self.assertEqual(feedback.validate_launcher_guard(metadata, profile, runtime)["profileSha256"],
                             metadata["guard"]["profileSha256"])
            for invalid in ({**metadata, "status": "stopped"},
                            {**metadata, "guard": {**metadata["guard"], "name": "unconfined"}},
                            {**metadata, "guard": {**metadata["guard"], "profileSha256": "0" * 64}},
                            {**metadata, "guard": {**metadata["guard"], "selfTest": "missing"}}):
                with self.subTest(invalid=invalid), self.assertRaises(feedback.FeedbackError):
                    feedback.validate_launcher_guard(invalid, profile, runtime)

    def test_feedback_role_requires_self_tested_pinned_paths_and_local_only_policy(self):
        import hashlib
        profile = b"profile bytes"
        metadata = {"roles": {"feedback-client": {
            "profileFile": "feedback-client.sb", "profileSha256": hashlib.sha256(profile).hexdigest(),
            "selfTest": "passed", "readDirectories": ["/cli", "/python"], "readFiles": ["/cli/passo"],
            "outbound": ["127.0.0.1:16080", "127.0.0.1:18081", "127.0.0.1:18089"], "inbound": []}}}
        expected = feedback.validate_feedback_role(metadata, profile, runtime_dir="/run", cli_repo="/cli",
                                                   python_prefix="/python", cli_binary="/cli/passo")
        self.assertEqual(expected["selfTest"], "passed")
        invalid_roles = [
            {**metadata["roles"]["feedback-client"], "selfTest": "failed"},
            {**metadata["roles"]["feedback-client"], "profileSha256": "0" * 64},
            {**metadata["roles"]["feedback-client"], "outbound": ["0.0.0.0:16080"]},
            {**metadata["roles"]["feedback-client"], "inbound": ["127.0.0.1:9999"]},
        ]
        for role in invalid_roles:
            with self.subTest(role=role), self.assertRaises(feedback.FeedbackError):
                feedback.validate_feedback_role({"roles": {"feedback-client": role}}, profile,
                                                runtime_dir="/run", cli_repo="/cli", python_prefix="/python",
                                                cli_binary="/cli/passo")

    def test_certified_workflow_sources_are_tracked_hashed_and_clean(self):
        with tempfile.TemporaryDirectory() as td:
            root = Path(td)
            for relative in feedback.HARNESS_SOURCES:
                path = root / relative
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_text("pinned " + relative)
            subprocess.run(["git", "init", "--quiet", str(root)], check=True)
            subprocess.run(["git", "-C", str(root), "config", "user.name", "Fixture Test"], check=True)
            subprocess.run(["git", "-C", str(root), "config", "user.email", "fixture@example.test"], check=True)
            subprocess.run(["git", "-C", str(root), "add", *feedback.HARNESS_SOURCES], check=True)
            subprocess.run(["git", "-C", str(root), "commit", "--quiet", "-m", "fixtures"], check=True)
            provenance = feedback.validate_harness_sources(root, root / feedback.HARNESS_SOURCES[0])
            self.assertEqual(set(provenance["sha256"]), set(feedback.HARNESS_SOURCES))
            self.assertEqual(provenance["paths"][feedback.HARNESS_SOURCES[1]],
                             (root / feedback.HARNESS_SOURCES[1]).resolve())
            (root / "scripts/specops-m1.py").write_text("changed")
            with self.assertRaises(feedback.FeedbackError):
                feedback.validate_harness_sources(root, root / feedback.HARNESS_SOURCES[0])

    def test_child_evidence_requires_canonical_public_state_not_result_envelope_alone(self):
        with tempfile.TemporaryDirectory() as td:
            root = Path(td)
            root.chmod(0o700)
            m0_path = root / "m0-smoke.json"
            m0_record = {"providerMode": "local", "authMode": "browser-pkce", "projectId": "101",
                         "backend": feedback.API, "projectName": "specops-local-feedback", "readCount": 2,
                         "first": {"stage": "BUILD.DEVELOP", "nextActionKey": "connect"},
                         "second": {"stage": "BUILD.DEVELOP", "nextActionKey": "connect"},
                         "secondReadNoMutation": True}
            m0_path.write_text(json.dumps(m0_record)); m0_path.chmod(0o600)
            envelope = {"result": "passed", "projectId": "101", "stage": "BUILD.DEVELOP",
                        "nextActionKey": "connect", "evidence": str(m0_path)}
            self.assertTrue(feedback.validate_child_evidence(
                envelope, phase="m0", evidence_root=root, project_id="101")["publicStateVerified"])
            m0_record["secondReadNoMutation"] = False
            m0_path.write_text(json.dumps(m0_record))
            with self.assertRaises(feedback.FeedbackError):
                feedback.validate_child_evidence(envelope, phase="m0", evidence_root=root, project_id="101")

    def test_child_evidence_rejects_wrong_file_or_noncanonical_m1_results(self):
        with tempfile.TemporaryDirectory() as td:
            root = Path(td); root.chmod(0o700)
            path = root / "m1-specops.json"
            acceptance = b"owner accepted fixture"
            import hashlib
            digest = hashlib.sha256(acceptance).hexdigest()
            outcomes = {"discoveryStatus": "succeeded", "discoveryRunId": "run-1", "docsGenerated": True,
                        "compilationRevision": 4, "repeatCompileSameIdentity": True,
                        "defineApply": "completed", "canonicalScopeStateVerified": True,
                        "journeyRead": "verified_project_scope", "fixtureSourceLabel": "fixture_source_not_repository_proof",
                        "fixtureSourceSha256": feedback.FIXTURE_SOURCE_SHA256,
                        "fixtureRepositoryCommit": feedback.FIXTURE_COMMIT,
                        "fixtureDocumentSourceLabel": "user_acceptance_fixture_not_repository_proof",
                        "fixtureContentSha256": digest, "fixtureDocumentFileName": f"specops-acceptance-{digest[:16]}.md",
                        "fixtureDocumentSizeBytes": len(acceptance),
                        "fixtureDocument": "uploaded_once_and_verified_unique_ready_filename_size_match",
                        "documentId": "doc-1", "compilationId": "comp-1"}
            record = {"workflow": "M1-local-specops-fixture-walk", "providerMode": "local",
                      "authMode": "browser-pkce", "projectId": "202", "projectName": "specops-local-fixture-walk",
                      "fixtureSourceSha256": feedback.FIXTURE_SOURCE_SHA256,
                      "sourceLabel": "fixture_source_not_repository_proof", "outcomes": outcomes}
            path.write_text(json.dumps(record)); path.chmod(0o600)
            envelope = {"result": "passed", "projectId": "202", "runId": "run-1",
                        "compilationRevision": 4, "evidence": str(path)}
            self.assertTrue(feedback.validate_child_evidence(
                envelope, phase="m1", evidence_root=root, project_id="202", run_id="run-1",
                compilation_revision=4, source_sha256=feedback.FIXTURE_SOURCE_SHA256,
                acceptance_sha256=digest)["publicStateVerified"])
            outcomes["canonicalScopeStateVerified"] = False
            path.write_text(json.dumps(record))
            with self.assertRaises(feedback.FeedbackError):
                feedback.validate_child_evidence(
                    envelope, phase="m1", evidence_root=root, project_id="202", run_id="run-1",
                    compilation_revision=4, source_sha256=feedback.FIXTURE_SOURCE_SHA256,
                    acceptance_sha256=digest)

    def test_child_evidence_requires_phase_specific_filename(self):
        with tempfile.TemporaryDirectory() as td:
            root = Path(td); root.chmod(0o700)
            wrong = root / "forged.json"
            wrong.write_text(json.dumps({"providerMode": "local"})); wrong.chmod(0o600)
            with self.assertRaises(feedback.FeedbackError):
                feedback.validate_child_evidence({"evidence": str(wrong)}, phase="m0",
                                                 evidence_root=root, project_id="1")

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
        with mock.patch.object(feedback, "_run_fixed", side_effect=[
                "desktop-linux", "unix:///var/run/docker.sock", "abc123",
                "pyx-sandbox\tdb\t[{\"HostIp\":\"127.0.0.1\",\"HostPort\":\"15432\"}]\t{\"pyx-sandbox-specops-internal\":{}}", "true"]) as run:
            self.assertTrue(feedback.verify_docker_db())
        self.assertEqual(run.call_count, 5)
        self.assertEqual(run.call_args_list[0].args[0][0], "docker")
        self.assertEqual(run.call_args_list[2].args[0][1], "ps")
        self.assertEqual(run.call_args_list[3].args[0][1], "inspect")
        self.assertEqual(run.call_args_list[4].args[0][1], "network")
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
            source = backend / "sandbox/specops/fixture/files/tinyGoApp.go"
            realm = backend / "sandbox/realm-export.json"
            acceptance = Path(__file__).resolve().parents[1] / "fixtures/specops-acceptance.md"
            for path in (source, realm):
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_text("fixture")
            cli = runtime / "passo"
            cli.write_text("cli"); cli.chmod(0o700)
            cli_repo = Path(__file__).resolve().parents[1]
            source_paths = {relative: cli_repo / relative for relative in feedback.HARNESS_SOURCES}
            source_hashes = {relative: feedback.sha256_file(path) for relative, path in source_paths.items()}
            provenance = {"paths": source_paths, "sha256": source_hashes}
            revisions = {"backend": "a" * 40, "database": "b" * 40, "cli": "c" * 40,
                         "cliTrackedDirty": False, "cliBinarySha256": feedback.sha256_file(cli),
                         "workflowSourceSha256": source_hashes}
            checks = {"cli": cli, "cliRepo": cli_repo, "backendRepo": backend, "runtimeDir": runtime,
                      "launcher": backend / "sandbox/specops/start-backend.py", "launcherSha256": "e" * 64,
                      "fixtures": {"realm": realm, "source": source, "sourceSha256": feedback.FIXTURE_SOURCE_SHA256},
                      "sourceProvenance": provenance, "revisions": revisions,
                      "checks": {"readyz": "http_200", "dockerDatabase": "ready", "nativeBackend": "running",
                                 "backendGuard": {"name": feedback.GUARD_NAME, "version": feedback.GUARD_VERSION,
                                                  "profileSha256": "d" * 64}},
                      "preflightSeconds": 0.1}
            args = SimpleNamespace(to="m1", max_runtime=300, runtime_dir=runtime)
            children = runtime / "children"
            m0_path = children / "m0-smoke.json"
            m1_path = children / "m1-specops.json"
            acceptance_hash = feedback.sha256_file(acceptance)
            m0_path.parent.mkdir(mode=0o700)
            m0_record = {"providerMode": "local", "authMode": "browser-pkce", "projectId": "1",
                         "backend": feedback.API, "projectName": "specops-local-feedback", "readCount": 2,
                         "first": {"stage": "BUILD.DEVELOP", "nextActionKey": "connect"},
                         "second": {"stage": "BUILD.DEVELOP", "nextActionKey": "connect"},
                         "secondReadNoMutation": True}
            outcomes = {"discoveryStatus": "succeeded", "discoveryRunId": "run-1", "docsGenerated": False,
                        "compilationRevision": 3, "repeatCompileSameIdentity": True, "defineApply": "completed",
                        "canonicalScopeStateVerified": True, "journeyRead": "verified_project_scope",
                        "fixtureSourceLabel": "fixture_source_not_repository_proof",
                        "fixtureSourceSha256": feedback.FIXTURE_SOURCE_SHA256,
                        "fixtureRepositoryCommit": feedback.FIXTURE_COMMIT,
                        "fixtureDocumentSourceLabel": "user_acceptance_fixture_not_repository_proof",
                        "fixtureContentSha256": acceptance_hash,
                        "fixtureDocumentFileName": f"specops-acceptance-{acceptance_hash[:16]}.md",
                        "fixtureDocumentSizeBytes": acceptance.stat().st_size,
                        "fixtureDocument": "uploaded_once_and_verified_unique_ready_filename_size_match",
                        "documentId": "doc-1", "compilationId": "compile-1"}
            m1_record = {"workflow": "M1-local-specops-fixture-walk", "providerMode": "local",
                         "authMode": "browser-pkce", "projectId": "2",
                         "projectName": "specops-local-fixture-walk",
                         "fixtureSourceSha256": feedback.FIXTURE_SOURCE_SHA256,
                         "sourceLabel": "fixture_source_not_repository_proof", "outcomes": outcomes}
            for path, content in ((m0_path, m0_record), (m1_path, m1_record)):
                path.write_text(json.dumps(content)); path.chmod(0o600)
            m0 = {"result": "passed", "projectId": "1", "stage": "BUILD.DEVELOP",
                  "nextActionKey": "connect", "evidence": str(m0_path)}
            m1 = {"result": "passed", "projectId": "2", "runId": "run-1", "compilationRevision": 3,
                  "evidence": str(m1_path)}
            with mock.patch.object(feedback, "ensure_private_evidence_dir", return_value=runtime / "children"), \
                    mock.patch.object(feedback, "prepare_feedback_role", side_effect=lambda checks: checks["checks"].update(
                        feedbackClientGuard={"role": "feedback-client"})), \
                    mock.patch.object(feedback, "assert_execution_provenance"), \
                    mock.patch.object(feedback, "guarded_child_argv", side_effect=lambda checks, path, argv: argv), \
                    mock.patch.object(feedback, "run_child", side_effect=[m0, m1]) as run_child:
                result = feedback.run_walk(args, checks)
            self.assertEqual(result["phases"]["m1"]["status"], "passed")
            self.assertTrue(result["phases"]["m1"]["publicStateVerified"])
            first, second = [call.args[0] for call in run_child.call_args_list]
            for argv in (first, second):
                self.assertIn("--realm-fixture", argv)
                self.assertIn(str(realm), argv)
                self.assertIn("--evidence-dir", argv)
                self.assertIn("specops-local-feedback" if argv is first else "specops-local-fixture-walk", argv)
            self.assertIn("--source-fixture", second)
            self.assertIn(str(source), second)
            self.assertIn("--acceptance-fixture", second)

    def test_guarded_child_uses_only_verified_launcher_role_and_exact_python_mapping(self):
        with tempfile.TemporaryDirectory() as td:
            cli = Path(td) / "passo"; cli.write_text("cli"); cli.chmod(0o700)
            script = Path(__file__).resolve().with_name("specops-smoke.py")
            checks = {"backendRepo": Path(td) / "backend", "runtimeDir": Path(td) / "runtime",
                      "launcher": Path(td) / "backend/sandbox/specops/start-backend.py",
                      "cliRepo": Path(__file__).resolve().parents[1], "cli": cli}
            command = [str(__import__("sys").executable), str(script), "--backend", feedback.API]
            with mock.patch.object(feedback.sys, "platform", "darwin"):
                argv = feedback.guarded_child_argv(checks, script, command)
            self.assertIn("guarded-exec", argv)
            self.assertIn("feedback", argv)
            self.assertIn(str(cli.resolve()), argv)
            shim = argv[argv.index("-c") + 1]
            self.assertIn("host=='sso.localtest.me'", shim)
            self.assertIn("original('127.0.0.1'", shim)
            self.assertIn("--read-dir", argv)


if __name__ == "__main__":
    unittest.main()
