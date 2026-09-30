#!/usr/bin/env python3
"""Run an M0 feedback-loop smoke against the real local PyxCloud API."""
from __future__ import annotations

import argparse
import base64
import hashlib
import html.parser
import json
import os
import secrets
import subprocess
import sys
import tempfile
import time
from pathlib import Path
from urllib.parse import urlencode, urljoin, urlparse, parse_qs

import requests


DEFAULT_API = "http://127.0.0.1:16080"
DEFAULT_ISSUER = "http://sso.localtest.me:18081/realms/passobuild"
CALLBACK = "http://127.0.0.1:51903/callback"


def local_http_url(raw: str, *, allow_sso: bool = False) -> str:
    u = urlparse(raw)
    host = (u.hostname or "").lower()
    if u.scheme != "http" or u.username or u.password or u.query or u.fragment:
        raise ValueError("only plain local HTTP URLs without credentials/query/fragment are allowed")
    if host not in {"127.0.0.1", "localhost", "::1"} and not (allow_sso and host == "sso.localtest.me"):
        raise ValueError("remote URL refused")
    return raw.rstrip("/")


class FormParser(html.parser.HTMLParser):
    def __init__(self):
        super().__init__()
        self.action = ""
        self.fields: dict[str, str] = {}

    def handle_starttag(self, tag, attrs):
        a = dict(attrs)
        if tag == "form" and not self.action:
            self.action = a.get("action", "")
        if tag == "input" and a.get("name"):
            self.fields[a["name"]] = a.get("value", "")


def fixture_credentials(path: Path) -> tuple[str, str]:
    realm = json.loads(path.read_text())
    for user in realm.get("users", []):
        for credential in user.get("credentials", []):
            if credential.get("type") == "password" and credential.get("value"):
                return user["username"], credential["value"]
    raise ValueError("realm fixture has no password credential")


def validate_form_action(action: str, page_url: str, issuer: str) -> str:
    resolved = urljoin(page_url, action)
    actual, expected = urlparse(resolved), urlparse(issuer)
    if (actual.scheme, actual.hostname, actual.port) != (expected.scheme, expected.hostname, expected.port):
        raise ValueError("login form action origin mismatch")
    if actual.username or actual.password or actual.fragment:
        raise ValueError("invalid login form action")
    return resolved


def browser_pkce_token(issuer: str, username: str, password: str) -> str:
    issuer = local_http_url(issuer, allow_sso=True)
    verifier = secrets.token_urlsafe(64)
    challenge = base64.urlsafe_b64encode(hashlib.sha256(verifier.encode()).digest()).decode().rstrip("=")
    state = secrets.token_urlsafe(32)
    authorize = issuer + "/protocol/openid-connect/auth?" + urlencode({
        "client_id": "passo-cli", "response_type": "code", "scope": "openid",
        "redirect_uri": CALLBACK, "code_challenge": challenge,
        "code_challenge_method": "S256", "state": state,
    })
    s = requests.Session()
    s.trust_env = False
    page = s.get(authorize, timeout=15, allow_redirects=False)
    page.raise_for_status()
    if urlparse(page.url).netloc != urlparse(issuer).netloc:
        raise RuntimeError("authorization endpoint origin mismatch")
    form = FormParser()
    form.feed(page.text)
    if not form.action:
        raise RuntimeError("login form missing")
    form_action = validate_form_action(form.action, page.url, issuer)
    form.fields.update({"username": username, "password": password})
    submitted = s.post(form_action, data=form.fields, timeout=15, allow_redirects=False)
    location = submitted.headers.get("Location", "")
    if submitted.status_code not in (302, 303) or not location:
        raise RuntimeError("login did not redirect to callback")
    cb = urlparse(location)
    expected = urlparse(CALLBACK)
    params = parse_qs(cb.query)
    if (cb.scheme, cb.hostname, cb.port, cb.path) != (expected.scheme, expected.hostname, expected.port, expected.path):
        raise RuntimeError("OAuth callback origin/path mismatch")
    if params.get("state", [None])[0] != state or not params.get("code", [""])[0]:
        raise RuntimeError("OAuth callback state/code invalid")
    token_response = s.post(issuer + "/protocol/openid-connect/token", data={
        "grant_type": "authorization_code", "client_id": "passo-cli",
        "code": params["code"][0], "redirect_uri": CALLBACK, "code_verifier": verifier,
    }, timeout=15, allow_redirects=False)
    if token_response.status_code != 200:
        raise RuntimeError("token exchange failed")
    token = token_response.json().get("access_token")
    if not token:
        raise RuntimeError("token endpoint omitted access token")
    return token


def project_rows(response: requests.Response):
    body = response.json()
    if isinstance(body, list):
        return body
    if isinstance(body, dict):
        for key in ("data", "projects", "items"):
            value = body.get(key)
            if isinstance(value, list):
                return value
            if isinstance(value, dict) and isinstance(value.get("projects"), list):
                return value["projects"]
    raise ValueError("unrecognized project list response")


def project_identity(row):
    return str(row.get("name", "")), row.get("id", row.get("projectId"))


def child_env(token: str, api: str, issuer: str):
    env = os.environ.copy()
    env.update({"PASSO_ACCESS_TOKEN": token, "PASSO_API_URL": api, "PASSO_ISSUER_URL": issuer})
    return env


def matching_projects(rows, project_name):
    return [(name, pid) for name, pid in map(project_identity, rows) if name == project_name]


def unique_project_id(matches):
    if len(matches) > 1:
        raise ValueError("duplicate project name")
    return str(matches[0][1]) if matches else None


def create_project(cli: str, token: str, api: str, issuer: str, project_name: str, td: str):
    input_file = Path(td) / "project.json"
    ledger = Path(td) / "creation-ledger.json"
    evidence = Path(td) / "creation-evidence"
    input_file.write_text(json.dumps({"name": project_name, "description": "Local M0 feedback loop"}))
    os.chmod(input_file, 0o600)
    env = child_env(token, api, issuer)
    created = subprocess.run([cli, "--profile", "sandbox", "--ledger", str(ledger), "--evidence-dir", str(evidence),
                              "projects", "create", "--input", str(input_file), "--json"],
                             env=env, stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=60)
    if created.returncode:
        raise RuntimeError("CLI project creation failed")


def report_failure(exc: Exception):
    print("M0 smoke failed: " + type(exc).__name__, file=sys.stderr)


def compact_status(raw: bytes, project_id: str):
    result = json.loads(raw)
    if result.get("schemaVersion") != 1 or result.get("profile") != "sandbox":
        raise ValueError("status schema/profile mismatch")
    if str(result.get("projectId")) != str(project_id):
        raise ValueError("status project mismatch")
    if not isinstance(result.get("stage"), str) or not result["stage"]:
        raise ValueError("status stage missing")
    action = result.get("nextAction")
    if not isinstance(action, dict) or not isinstance(action.get("key"), str) or not action["key"]:
        raise ValueError("status nextAction.key missing")
    return {"schemaVersion": 1, "profile": "sandbox", "projectId": str(project_id),
            "status": result.get("status"), "stage": result["stage"], "nextActionKey": action["key"]}


def invoke_status(cli: str, token: str, api: str, issuer: str, project_id: str, ledger: Path, evidence: Path):
    env = child_env(token, api, issuer)
    cmd = [cli, "--profile", "sandbox", "--project", str(project_id), "--ledger", str(ledger),
           "--evidence-dir", str(evidence), "--json", "status"]
    proc = subprocess.run(cmd, env=env, stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=60)
    if proc.returncode:
        raise RuntimeError("CLI status failed (output suppressed to protect credentials)")
    return compact_status(proc.stdout, project_id)


def tree_fingerprint(root: Path):
    if not root.exists():
        return None
    h = hashlib.sha256()
    for p in sorted(root.rglob("*")):
        if p.is_file():
            h.update(str(p.relative_to(root)).encode())
            h.update(p.read_bytes())
    return h.hexdigest()


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--cli", required=True, help="path to integrated passo CLI executable")
    ap.add_argument("--backend", default=DEFAULT_API)
    ap.add_argument("--issuer", default=DEFAULT_ISSUER)
    default_fixture = Path(__file__).resolve().parents[2] / "specops-backend/sandbox/realm-export.json"
    ap.add_argument("--realm-fixture", type=Path, default=default_fixture)
    ap.add_argument("--evidence-dir", type=Path, required=True)
    ap.add_argument("--project-name", default="specops-local-feedback")
    args = ap.parse_args()
    api = local_http_url(args.backend)
    issuer = local_http_url(args.issuer, allow_sso=True)
    username, password = fixture_credentials(args.realm_fixture)
    token = browser_pkce_token(issuer, username, password)
    rest = requests.Session()
    rest.trust_env = False
    headers = {"Authorization": "Bearer " + token}
    listing = rest.get(api + "/vibe/projects", headers=headers, timeout=15, allow_redirects=False)
    if listing.status_code != 200:
        raise RuntimeError("project list request failed")
    matches = matching_projects(project_rows(listing), args.project_name)
    project_id = unique_project_id(matches)
    if not project_id:
        # Creation is deliberately delegated to the integrated CLI; never mutate through REST here.
        with tempfile.TemporaryDirectory() as td:
            create_project(args.cli, token, api, issuer, args.project_name, td)
            fresh = rest.get(api + "/vibe/projects", headers=headers, timeout=15, allow_redirects=False)
            if fresh.status_code != 200:
                raise RuntimeError("project list request failed after creation")
            matches = matching_projects(project_rows(fresh), args.project_name)
            project_id = unique_project_id(matches)
            if not project_id:
                raise RuntimeError("created project absent from public project list")
    if not project_id.isdigit() or int(project_id) <= 0:
        raise RuntimeError("invalid project ID")

    args.evidence_dir.mkdir(parents=True, exist_ok=True)
    args.evidence_dir.chmod(0o700)
    with tempfile.TemporaryDirectory(prefix="specops-m0-") as td:
        ledger = Path(td) / "ledger.json"
        evidence = Path(td) / "cli-evidence"
        first = invoke_status(args.cli, token, api, issuer, project_id, ledger, evidence)
        before = (tree_fingerprint(ledger.parent), tree_fingerprint(evidence))
        second = invoke_status(args.cli, token, api, issuer, project_id, ledger, evidence)
        after = (tree_fingerprint(ledger.parent), tree_fingerprint(evidence))
        if first != second or before != after:
            raise RuntimeError("second status read changed output or local workflow state")
    record = {"recordedAt": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
              "providerMode": "local", "backend": "127.0.0.1:16080", "authMode": "browser-pkce",
              "projectId": project_id, "projectName": args.project_name, "readCount": 2,
              "first": first, "second": second, "secondReadNoMutation": True}
    out = args.evidence_dir / "m0-smoke.json"
    out.write_text(json.dumps(record, indent=2) + "\n")
    out.chmod(0o600)
    print(json.dumps({"result": "passed", "projectId": project_id, "stage": first["stage"],
                      "nextActionKey": first["nextActionKey"], "evidence": str(out)}))


if __name__ == "__main__":
    try:
        main()
    except Exception as exc:
        report_failure(exc)
        sys.exit(1)
