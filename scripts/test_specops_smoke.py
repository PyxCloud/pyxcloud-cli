import importlib.util
import io
import tempfile
import unittest
from unittest import mock
from pathlib import Path

SCRIPT = Path(__file__).with_name("specops-smoke.py")
spec = importlib.util.spec_from_file_location("specops_smoke", SCRIPT)
smoke = importlib.util.module_from_spec(spec)
spec.loader.exec_module(smoke)


class SmokeHarnessTests(unittest.TestCase):
    def test_url_guard_accepts_only_local_http(self):
        self.assertEqual(smoke.local_http_url("http://127.0.0.1:16080"), "http://127.0.0.1:16080")
        self.assertEqual(smoke.local_http_url("http://sso.localtest.me:18081", allow_sso=True),
                         "http://sso.localtest.me:18081")
        for value in ("https://127.0.0.1", "http://example.com", "http://user@127.0.0.1", "http://127.0.0.1/?x=1"):
            with self.subTest(value=value), self.assertRaises(ValueError):
                smoke.local_http_url(value)
        with self.assertRaises(ValueError):
            smoke.local_http_url("http://sso.localtest.me:18081")

    def test_status_projection_is_minimal_and_token_free(self):
        raw = b'{"schemaVersion":1,"profile":"sandbox","projectId":1,"status":"observed","stage":"board","nextAction":{"key":"open_board"},"data":{"secret":"never retain"}}'
        record = smoke.compact_status(raw, "1")
        self.assertEqual(record["nextActionKey"], "open_board")
        self.assertNotIn("data", record)
        self.assertNotIn("secret", str(record))
        self.assertNotIn("access_token", str(record))

    def test_status_rejects_bad_project_and_action(self):
        for raw in (b'{"schemaVersion":1,"profile":"sandbox","projectId":2,"stage":"board","nextAction":{"key":"x"}}',
                    b'{"schemaVersion":1,"profile":"sandbox","projectId":1,"stage":"board","nextAction":{}}'):
            with self.subTest(raw=raw), self.assertRaises(ValueError):
                smoke.compact_status(raw, "1")

    def test_remote_form_action_is_rejected_before_credentials_post(self):
        class FakeSession:
            def __init__(self):
                self.trust_env = True
                self.posts = []

            def get(self, *args, **kwargs):
                class Response:
                    url = "http://sso.localtest.me:18081/realms/passobuild/login"
                    text = '<form action="https://attacker.example/collect"><input name="csrf" value="x"></form>'
                    def raise_for_status(self): pass
                return Response()

            def post(self, *args, **kwargs):
                self.posts.append(args)
                raise AssertionError("credentials must not be posted")

        fake = FakeSession()
        with mock.patch.object(smoke.requests, "Session", return_value=fake):
            with self.assertRaises(ValueError):
                smoke.browser_pkce_token("http://sso.localtest.me:18081/realms/passobuild", "u", "secret")
        self.assertFalse(fake.posts)
        self.assertFalse(fake.trust_env)

    def test_child_cli_receives_matching_api_and_issuer(self):
        env = smoke.child_env("token-value", "http://127.0.0.1:16080", "http://sso.localtest.me:18081/realms/passobuild")
        self.assertEqual(env["PASSO_API_URL"], "http://127.0.0.1:16080")
        self.assertEqual(env["PASSO_ISSUER_URL"], "http://sso.localtest.me:18081/realms/passobuild")
        self.assertEqual(env["PASSO_ACCESS_TOKEN"], "token-value")

    def test_project_creation_has_isolated_ledger_and_evidence(self):
        with tempfile.TemporaryDirectory() as td, mock.patch.object(smoke.subprocess, "run") as run:
            run.return_value.returncode = 0
            smoke.create_project("/tmp/passo", "token", "http://127.0.0.1:16080", "http://sso.localtest.me:18081/realm", "p", td)
            args, kwargs = run.call_args
            self.assertIn(str(Path(td) / "creation-ledger.json"), args[0])
            self.assertIn(str(Path(td) / "creation-evidence"), args[0])
            self.assertEqual(kwargs["env"]["PASSO_API_URL"], "http://127.0.0.1:16080")

    def test_failure_report_suppresses_exception_text(self):
        output = io.StringIO()
        with mock.patch.object(smoke.sys, "stderr", output):
            smoke.report_failure(RuntimeError("https://host/?access_token=secret"))
        self.assertEqual(output.getvalue(), "M0 smoke failed: RuntimeError\n")

    def test_duplicate_project_names_fail_closed(self):
        rows = [{"id": 1, "name": "same"}, {"id": 2, "name": "same"}]
        with self.assertRaises(ValueError):
            smoke.unique_project_id(smoke.matching_projects(rows, "same"))
        self.assertIsNone(smoke.unique_project_id(smoke.matching_projects(rows, "missing")))


if __name__ == "__main__":
    unittest.main()
