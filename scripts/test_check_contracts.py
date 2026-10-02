import json
import pathlib
import shutil
import subprocess
import sys
import tempfile
import unittest


ROOT = pathlib.Path(__file__).resolve().parents[1]
CHECKER = ROOT / "scripts" / "check-contracts.py"


class BackendContractCheckTest(unittest.TestCase):
    def test_extra_contract_and_changed_hash_fail(self):
        lock = json.loads((ROOT / "contracts" / "backend.lock.json").read_text())
        with tempfile.TemporaryDirectory() as temporary:
            contracts = pathlib.Path(temporary) / "contracts"
            contracts.mkdir()
            for name in lock["files"]:
                shutil.copyfile(ROOT / "contracts" / name, contracts / name)

            changed = next(iter(lock["files"]))
            with (contracts / changed).open("ab") as contract:
                contract.write(b" ")
            (contracts / "new.openapi.json").write_text("{}")

            result = subprocess.run(
                [sys.executable, str(CHECKER), "--backend", str(contracts)],
                capture_output=True,
                text=True,
                check=False,
            )
            self.assertEqual(result.returncode, 1)
            self.assertIn("new backend OpenAPI file: new.openapi.json", result.stderr)
            self.assertIn(f"backend contract drift: {changed}", result.stderr)


if __name__ == "__main__":
    unittest.main()
