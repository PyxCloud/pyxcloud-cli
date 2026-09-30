import importlib.util
import unittest
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


if __name__ == "__main__":
    unittest.main()
