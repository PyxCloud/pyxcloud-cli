#!/usr/bin/env python3
"""Run the bounded M1 discovery → docs → compilation → scope feedback loop."""
from __future__ import annotations

import argparse
import hashlib
import importlib.util
import json
import os
import subprocess
import sys
import tempfile
import time
from pathlib import Path

import requests


DEFAULT_API = "http://127.0.0.1:16080"
DEFAULT_ISSUER = "http://sso.localtest.me:18081/realms/passobuild"
DEFAULT_PROJECT = "specops-local-fixture-walk"
FIXTURE_REPO = "sandbox/passo-v1-fixture"
FIXTURE_COMMIT = "63870611a7085633e8f1c2dae62d8e2b6fdf0e53"
FIXTURE_SOURCE_SHA256 = "6cdabec6ded28d5a648a316c78f7daf9936484c0691668e435f9f6095e1a3556"
POLL_LIMIT = 20
POLL_WAIT_SECONDS = 20
POLL_INTERVAL_SECONDS = 10
TOTAL_TIMEOUT_SECONDS = 900


def smoke_helpers():
    path = Path(__file__).with_name("specops-smoke.py")
    spec = importlib.util.spec_from_file_location("specops_smoke", path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def validate_envelope(value, project_id):
    if not isinstance(value, dict) or value.get("schemaVersion") != 1:
        raise ValueError("invalid CLI envelope")
    if value.get("profile") != "sandbox" or str(value.get("projectId")) != str(project_id):
        raise ValueError("CLI envelope scope mismatch")
    data = value.get("data")
    if isinstance(data, str):
        try:
            data = json.loads(data)
        except json.JSONDecodeError as exc:
            raise ValueError("invalid CLI data envelope") from exc
    if not isinstance(data, dict):
        raise ValueError("CLI data object missing")
    return data


def _walk(value):
    if isinstance(value, dict):
        yield value
        for child in value.values():
            yield from _walk(child)
    elif isinstance(value, list):
        for child in value:
            yield from _walk(child)


def validate_succeeded_report(value, repository=FIXTURE_REPO, commit_sha=FIXTURE_COMMIT):
    candidates = list(_walk(value))
    run_id = next((row.get("runId") for row in candidates if isinstance(row.get("runId"), str) and row["runId"]), None)
    succeeded = any(row.get("status") == "succeeded" for row in candidates)
    snapshots = [row.get("sourceSnapshot") for row in candidates if isinstance(row.get("sourceSnapshot"), dict)]
    valid_snapshot = any(
        isinstance(snapshot.get("repositories"), list)
        and any(isinstance(repo, dict) and repo.get("identity") == repository and repo.get("commitSha") == commit_sha for repo in snapshot["repositories"])
        for snapshot in snapshots
    )
    if not run_id or not succeeded or not valid_snapshot:
        raise ValueError("discovery is not succeeded with valid source provenance")
    return {"runId": run_id}


def validate_run_output(value, project_id, target):
    if not isinstance(value, dict) or value.get("schemaVersion") != 1:
        raise ValueError("invalid CLI run result")
    if value.get("status") != "completed" or value.get("stage") != target:
        raise ValueError("CLI run plan did not complete its requested stage")
    if not str(project_id).isdigit() or int(project_id) <= 0:
        raise ValueError("invalid run project scope")
    return value


def unique_fixture_document(rows, filename, size):
    if not isinstance(rows, list):
        raise ValueError("document list unavailable")
    matches = [row for row in rows if isinstance(row, dict) and row.get("fileName") == filename]
    if len(matches) != 1:
        raise ValueError("fixture document filename is absent or ambiguous")
    row = matches[0]
    try:
        remote_size = int(row.get("sizeBytes", -1))
    except (TypeError, ValueError):
        remote_size = -1
    if remote_size != size or str(row.get("status", "")).upper() != "READY" or not row.get("id"):
        raise ValueError("existing fixture document cannot be verified")
    return row


def terminal_status(data):
    for row in _walk(data):
        status = row.get("status")
        if isinstance(status, str):
            return status.lower()
    return ""


def find_value(data, key):
    for row in _walk(data):
        if key in row:
            return row[key]
    return None


def validate_nonempty_scope(data):
    contract = find_value(data, "scope_contract")
    if not isinstance(contract, dict):
        raise ValueError("scope derivation omitted its contract")
    result = contract.get("result")
    if not isinstance(result, dict):
        raise ValueError("scope derivation omitted its result")
    requirements = result.get("requirements")
    invariants = result.get("invariants")
    if not isinstance(requirements, list) or not requirements or not isinstance(invariants, list) or not invariants:
        raise ValueError("scope derivation has no functional requirement or invariant")
    ids = {row.get("id") for row in requirements + invariants if isinstance(row, dict)}
    if not {"FR-01", "INV-01"}.issubset(ids):
        raise ValueError("derived scope does not contain the explicit fixture requirements")
    candidate_id = contract.get("candidateId")
    if not isinstance(candidate_id, str) or not candidate_id:
        raise ValueError("scope derivation omitted candidate identity")
    return candidate_id


def compile_input(document_id):
    if not isinstance(document_id, str) or not document_id.strip():
        raise ValueError("fixture document ID is missing")
    # The CLI owns idempotencyKey; this identical canonical body lets it derive
    # the same managed key on a replay without sending the forbidden key itself.
    return {"documentIds": [document_id]}


def find_fixture_backend(script_path):
    for ancestor in Path(script_path).resolve().parents:
        for relative in (".worktrees/specops-backend/sandbox", "platform/pyx-backend/sandbox"):
            candidate = ancestor / relative
            if ((candidate / "realm-export.json").is_file()
                    and (candidate / "specops/fixture/files/tinyGoApp.go").is_file()):
                return candidate
    raise FileNotFoundError("workspace fixture checkout not found; pass explicit fixture paths")


def sha256_file(path):
    h = hashlib.sha256()
    with path.open("rb") as source:
        for chunk in iter(lambda: source.read(65536), b""):
            h.update(chunk)
    return h.hexdigest()


class Harness:
    def __init__(self, args, token, project_id, tempdir, rest, deadline, fixture_commit):
        self.args = args
        self.token = token
        self.project_id = str(project_id)
        self.tempdir = Path(tempdir)
        self.ledger = self.tempdir / "ledger.json"
        self.cli_evidence = self.tempdir / "cli-evidence"
        self.env = smoke_helpers().child_env(token, args.api, args.issuer)
        self.rest = rest
        self.deadline = deadline
        self.fixture_commit = fixture_commit
        self.timings = {}
        self.outcomes = {}

    def cli(self, *argv, timeout=60):
        remaining = self.deadline - time.monotonic()
        if remaining <= 0:
            raise TimeoutError("overall M1 runtime deadline exceeded")
        timeout = min(timeout, max(1, remaining))
        cmd = [self.args.cli, "--profile", "sandbox", "--project", self.project_id,
               "--ledger", str(self.ledger), "--evidence-dir", str(self.cli_evidence), "--json", *argv]
        started = time.monotonic()
        try:
            proc = subprocess.run(cmd, env=self.env, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                                  timeout=timeout, check=False)
        except subprocess.TimeoutExpired as exc:
            raise RuntimeError("CLI command deadline exceeded") from exc
        finally:
            self.timings[argv[0]] = self.timings.get(argv[0], 0.0) + (time.monotonic() - started)
        if proc.returncode != 0:
            raise RuntimeError("CLI command failed: " + argv[0])
        try:
            envelope = json.loads(proc.stdout)
        except (json.JSONDecodeError, UnicodeDecodeError) as exc:
            raise ValueError("CLI returned an invalid JSON envelope") from exc
        if argv[0] == "run":
            return envelope, validate_run_output(envelope, self.project_id, "connect")
        return envelope, validate_envelope(envelope, self.project_id)

    def public_json(self, method, path, **kwargs):
        remaining = self.deadline - time.monotonic()
        if remaining <= 0:
            raise TimeoutError("overall M1 runtime deadline exceeded")
        url = self.args.api + path
        started = time.monotonic()
        try:
            response = self.rest.request(method, url, timeout=min(15, max(1, remaining)), allow_redirects=False, **kwargs)
        finally:
            self.timings["fixtureBootstrap"] = self.timings.get("fixtureBootstrap", 0.0) + (time.monotonic() - started)
        if response.status_code not in (200, 201):
            raise RuntimeError("fixture bootstrap API request failed")
        try:
            return response.json()
        except requests.exceptions.JSONDecodeError as exc:
            raise ValueError("fixture bootstrap returned invalid JSON") from exc

    def ensure_fixture_document(self, fixture_path):
        raw = fixture_path.read_bytes()
        size, digest = len(raw), hashlib.sha256(raw).hexdigest()
        filename = f"specops-acceptance-{digest[:16]}.md"
        url = f"/vibe/projects/{self.project_id}/documents"
        listing = self.public_json("GET", url)
        rows = listing.get("documents") if isinstance(listing, dict) else None
        if not isinstance(rows, list):
            raise ValueError("project document listing cannot establish upload safety")
        named = [row for row in rows or [] if isinstance(row, dict) and row.get("fileName") == filename]
        if named:
            doc = unique_fixture_document(rows, filename, size)
            self.outcomes["fixtureDocument"] = "reused_unique_ready_filename_size_match"
        else:
            # This REST write exists only to bootstrap this local fixture source.
            # A network-uncertain upload is terminal: the harness never retries it.
            uploaded = self.public_json("POST", url, files={"file": (filename, raw, "text/markdown")})
            if not isinstance(uploaded, dict) or uploaded.get("fileName") != filename:
                raise ValueError("fixture upload response cannot be verified")
            refreshed = self.public_json("GET", url)
            refreshed_rows = refreshed.get("documents") if isinstance(refreshed, dict) else None
            if not isinstance(refreshed_rows, list):
                raise ValueError("uploaded fixture cannot be verified from document listing")
            doc = unique_fixture_document(refreshed_rows, filename, size)
            if str(doc.get("id")) != str(uploaded.get("id")):
                raise ValueError("fixture upload result differs from listed document")
            self.outcomes["fixtureDocument"] = "uploaded_once_and_verified_unique_ready_filename_size_match"
        self.outcomes["fixtureContentSha256"] = digest
        self.outcomes["fixtureDocumentFileName"] = filename
        self.outcomes["fixtureDocumentSizeBytes"] = size
        self.outcomes["fixtureSourceLabel"] = "user_acceptance_fixture_not_repository_proof"
        return str(doc["id"])

    def discover(self):
        _, data = self.cli("discover", "read", "--wait", str(POLL_WAIT_SECONDS), timeout=35)
        status = terminal_status(data)
        if not status:
            self.cli("discover", "start")
            self.outcomes["discoveryStarted"] = True
        else:
            self.outcomes["discoveryStarted"] = False
        for attempt in range(POLL_LIMIT):
            if attempt or status in {"queued", "running", "in_progress", "processing"} or not status:
                _, data = self.cli("discover", "read", "--wait", str(POLL_WAIT_SECONDS), timeout=35)
                status = terminal_status(data)
            if status == "succeeded":
                valid = validate_succeeded_report(data, FIXTURE_REPO, self.fixture_commit)
                self.outcomes["discoveryRunId"] = valid["runId"]
                self.outcomes["discoveryStatus"] = status
                return valid["runId"]
            if status in {"failed", "stale", "cancelled", "error"}:
                raise RuntimeError("discovery ended with a non-success status")
            time.sleep(POLL_INTERVAL_SECONDS)
        raise TimeoutError("discovery polling limit exceeded")

    def docs_for_run(self, run_id):
        _, current = self.cli("docs", "read")
        if self._docs_state(current, run_id) == "current":
            self.outcomes["docsGenerated"] = False
            return
        input_path = self.tempdir / "docs-generate.json"
        input_path.write_text(json.dumps({"runId": run_id}), encoding="utf-8")
        os.chmod(input_path, 0o600)
        self.cli("docs", "generate", "--input", str(input_path))
        for _ in range(POLL_LIMIT):
            _, current = self.cli("docs", "read")
            state = self._docs_state(current, run_id)
            if state == "current":
                self.outcomes["docsGenerated"] = True
                return
            if state == "failed":
                raise RuntimeError("documentation generation failed")
            time.sleep(POLL_INTERVAL_SECONDS)
        raise TimeoutError("documentation polling limit exceeded")

    def _docs_state(self, data, run_id):
        for row in _walk(data):
            status = row.get("documentation_status")
            if not isinstance(status, dict):
                continue
            state = str(status.get("state", "")).lower()
            if state == "failed":
                return "failed"
            if state == "succeeded" and status.get("book_run_id") == run_id and not status.get("stale", False):
                return "current"
        return "pending"

    def compile_and_derive(self, doc_id):
        body = compile_input(doc_id)
        input_path = self.tempdir / "compile.json"
        input_path.write_text(json.dumps(body, separators=(",", ":")), encoding="utf-8")
        os.chmod(input_path, 0o600)
        _, first = self.cli("docs", "compile", "--input", str(input_path))
        _, second = self.cli("docs", "compile", "--input", str(input_path))
        def identity(data):
            compilation_id = find_value(data, "compilationId")
            revision = find_value(data, "revision")
            if not compilation_id or not isinstance(revision, int) or revision <= 0:
                raise ValueError("compilation identity missing")
            return str(compilation_id), revision
        first_id, first_revision = identity(first)
        second_id, second_revision = identity(second)
        if (first_id, first_revision) != (second_id, second_revision):
            raise ValueError("idempotent compile changed compilation identity")
        self.outcomes["compilationId"] = first_id
        self.outcomes["compilationRevision"] = first_revision
        self.outcomes["repeatCompileSameIdentity"] = True
        _, derived = self.cli("define", "derive", timeout=120)
        contract_id = validate_nonempty_scope(derived)
        self.cli("define", "apply", timeout=120)
        self.outcomes["defineApply"] = "completed"
        # Verify the applied scope contract through the CLI run-plan surface. The
        # plan checks the canonical state read against the persisted derivation.
        plan = {
            "schemaVersion": 1,
            "steps": [{
                "stage": "connect", "operation": "projects:canonicalProjectStateRead",
                "params": {"projectId": "${projectId}"}, "query": {},
                "check": {"operation": "projects:canonicalProjectStateRead", "params": {"projectId": "${projectId}"},
                          "query": {}, "pointer": "/data/scope_contract/candidateId", "equals": contract_id},
            }],
        }
        plan_path = self.tempdir / "state-check.json"
        plan_path.write_text(json.dumps(plan), encoding="utf-8")
        os.chmod(plan_path, 0o600)
        self.cli("run", "--plan", str(plan_path), "--to", "connect", timeout=90)
        self.outcomes["canonicalScopeStateVerified"] = True
        _, journey = self.cli("status")
        if str(find_value(journey, "projectId")) != self.project_id:
            raise ValueError("journey project scope mismatch")
        self.outcomes["journeyRead"] = "verified_project_scope"


def rows_for_projects(response):
    return smoke_helpers().project_rows(response)


def get_project_id(rest, api, name):
    response = rest.get(api + "/vibe/projects", timeout=15, allow_redirects=False)
    if response.status_code != 200:
        raise RuntimeError("project list request failed")
    matches = smoke_helpers().matching_projects(rows_for_projects(response), name)
    project_id = smoke_helpers().unique_project_id(matches)
    return project_id


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--cli", required=True, help="path to integrated passo CLI executable")
    ap.add_argument("--backend", default=DEFAULT_API)
    ap.add_argument("--issuer", default=DEFAULT_ISSUER)
    ap.add_argument("--realm-fixture", type=Path)
    ap.add_argument("--acceptance-fixture", type=Path, default=Path(__file__).resolve().parents[1] / "fixtures/specops-acceptance.md")
    ap.add_argument("--source-fixture", type=Path)
    ap.add_argument("--evidence-dir", type=Path, required=True)
    ap.add_argument("--project-name", default=DEFAULT_PROJECT)
    ap.add_argument("--max-runtime", type=int, default=TOTAL_TIMEOUT_SECONDS)
    args = ap.parse_args()
    if args.realm_fixture is None or args.source_fixture is None:
        fixture_backend = find_fixture_backend(__file__)
        args.realm_fixture = args.realm_fixture or fixture_backend / "realm-export.json"
        args.source_fixture = args.source_fixture or fixture_backend / "specops/fixture/files/tinyGoApp.go"
    helpers = smoke_helpers()
    api = helpers.local_http_url(args.backend)
    issuer = helpers.local_http_url(args.issuer, allow_sso=True)
    args.api, args.issuer = api, issuer
    if args.max_runtime < 60 or args.max_runtime > 1800:
        raise ValueError("max-runtime must be 60..1800 seconds")
    for fixture in (args.realm_fixture, args.acceptance_fixture, args.source_fixture):
        if not fixture.is_file():
            raise FileNotFoundError("required local fixture is missing")
    acceptance = args.acceptance_fixture.read_bytes()
    if b"FR-01" not in acceptance or b"INV-01" not in acceptance or b"/healthz" not in acceptance:
        raise ValueError("acceptance fixture lacks required functional/invariant facts")
    source_bytes = args.source_fixture.read_bytes()
    required_source_facts = (b'http.HandleFunc("/healthz"', b'os.Getenv("HEALTH_DB_ADDR")', b'net.DialTimeout("tcp", addr, time.Second)', b'http.StatusServiceUnavailable', b'"ok"')
    if not all(marker in source_bytes for marker in required_source_facts) or hashlib.sha256(source_bytes).hexdigest() != FIXTURE_SOURCE_SHA256:
        raise ValueError("health fixture source does not match the pinned acceptance facts")
    manifest_path = args.source_fixture.parents[1] / "manifest.json"
    manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
    if manifest.get("identity") != FIXTURE_REPO or manifest.get("commitSHA") != FIXTURE_COMMIT:
        raise ValueError("health fixture manifest identity/commit mismatch")
    username, password = helpers.fixture_credentials(args.realm_fixture)
    token = helpers.browser_pkce_token(issuer, username, password)
    rest = requests.Session()
    rest.trust_env = False
    rest.headers.update({"Authorization": "Bearer " + token})
    started = time.monotonic()
    deadline = started + args.max_runtime
    project_id = get_project_id(rest, api, args.project_name)
    if not project_id:
        with tempfile.TemporaryDirectory(prefix="specops-m1-create-") as td:
            helpers.create_project(args.cli, token, api, issuer, args.project_name, td)
        project_id = get_project_id(rest, api, args.project_name)
        if not project_id:
            raise RuntimeError("created project absent from public project list")
    if not str(project_id).isdigit() or int(project_id) <= 0:
        raise ValueError("invalid project ID")
    args.evidence_dir.mkdir(parents=True, exist_ok=True)
    args.evidence_dir.chmod(0o700)
    with tempfile.TemporaryDirectory(prefix="specops-m1-") as td:
        harness = Harness(args, token, project_id, td, rest, deadline, manifest["commitSHA"])
        source_hash = sha256_file(args.source_fixture)
        harness.outcomes["fixtureSourceSha256"] = source_hash
        harness.outcomes["fixtureSourceLabel"] = "fixture_source_not_repository_proof"
        harness.outcomes["fixtureRepositoryCommit"] = manifest["commitSHA"]
        harness.cli("connect", "attach", "--repo", FIXTURE_REPO)
        doc_id = harness.ensure_fixture_document(args.acceptance_fixture)
        harness.outcomes["documentId"] = doc_id
        run_id = harness.discover()
        harness.docs_for_run(run_id)
        harness.compile_and_derive(doc_id)
        elapsed = time.monotonic() - started
        if elapsed > args.max_runtime:
            raise TimeoutError("overall M1 runtime deadline exceeded")
        record = {
            "recordedAt": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
            "workflow": "M1-local-specops-fixture-walk", "providerMode": "local",
            "authMode": "browser-pkce", "projectId": str(project_id), "projectName": args.project_name,
            "fixtureSourceSha256": source_hash, "sourceLabel": "fixture_source_not_repository_proof",
            "outcomes": harness.outcomes, "phaseSeconds": {k: round(v, 3) for k, v in harness.timings.items()},
            "elapsedSeconds": round(elapsed, 3),
        }
    out = args.evidence_dir / "m1-specops.json"
    out.write_text(json.dumps(record, indent=2) + "\n", encoding="utf-8")
    out.chmod(0o600)
    print(json.dumps({"result": "passed", "projectId": str(project_id), "runId": record["outcomes"]["discoveryRunId"],
                      "compilationRevision": record["outcomes"]["compilationRevision"], "evidence": str(out)}))


if __name__ == "__main__":
    try:
        main()
    except Exception as exc:
        print("M1 harness failed: " + type(exc).__name__, file=sys.stderr)
        sys.exit(1)
