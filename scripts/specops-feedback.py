#!/usr/bin/env python3
"""Run read-only local feedback checks or the bounded M0/M1 CLI walk."""
from __future__ import annotations

import argparse
import hashlib
import json
import os
from pathlib import Path
import secrets
import signal
import stat
import subprocess
import sys
import time
from urllib.error import URLError
from urllib.request import ProxyHandler, Request, build_opener


API = "http://127.0.0.1:16080"
ISSUER = "http://sso.localtest.me:18081/realms/passobuild"
DB_PROJECT = "pyx-sandbox"
DB_SERVICE = "db"
DB_HOST_PORT = "15432"
FIXTURE_REPO = "sandbox/passo-v1-fixture"
FIXTURE_COMMIT = "63870611a7085633e8f1c2dae62d8e2b6fdf0e53"
FIXTURE_SOURCE_SHA256 = "6cdabec6ded28d5a648a316c78f7daf9936484c0691668e435f9f6095e1a3556"
OVERALL_TIMEOUT_DEFAULT = 900
OVERALL_TIMEOUT_MAX = 1800


class FeedbackError(Exception):
    """Safe-to-display local runner error."""


def _safe_env(*, docker=False):
    keys = ["PATH", "HOME", "TMPDIR", "LANG", "LC_ALL"]
    if docker:
        keys.extend(["DOCKER_HOST", "DOCKER_CONTEXT", "DOCKER_CONFIG", "DOCKER_CERT_PATH", "DOCKER_TLS_VERIFY"])
    env = {key: os.environ[key] for key in keys if key in os.environ}
    env["PYTHONDONTWRITEBYTECODE"] = "1"
    return env


def _run_fixed(argv, *, timeout, env=None):
    try:
        result = subprocess.run(argv, stdin=subprocess.DEVNULL, stdout=subprocess.PIPE,
                                stderr=subprocess.DEVNULL, timeout=timeout, check=False,
                                env=env or _safe_env())
    except (OSError, subprocess.TimeoutExpired):
        raise FeedbackError("a bounded local verification command failed") from None
    if result.returncode:
        raise FeedbackError("a bounded local verification command failed")
    return result.stdout.decode("utf-8", errors="strict").strip()


def repo_revision(path: Path):
    supplied = Path(path).expanduser()
    if supplied.is_symlink():
        raise FeedbackError("repository path must not be a symlink")
    try:
        root = supplied.resolve(strict=True)
    except OSError:
        raise FeedbackError("repository path is unavailable") from None
    if not root.is_dir():
        raise FeedbackError("repository path is not a directory")
    top = Path(_run_fixed(["git", "-C", str(root), "rev-parse", "--show-toplevel"], timeout=5)).resolve()
    if top != root:
        raise FeedbackError("repository path must name the checkout root")
    revision = _run_fixed(["git", "-C", str(root), "rev-parse", "HEAD"], timeout=5)
    if len(revision) != 40 or any(c not in "0123456789abcdef" for c in revision.lower()):
        raise FeedbackError("repository revision is invalid")
    dirty = bool(_run_fixed(["git", "-C", str(root), "status", "--porcelain", "--untracked-files=no"], timeout=5))
    return root, revision.lower(), dirty


def sha256_file(path: Path):
    digest = hashlib.sha256()
    try:
        with path.open("rb") as stream:
            for chunk in iter(lambda: stream.read(65536), b""):
                digest.update(chunk)
    except OSError:
        raise FeedbackError("a required local fixture is unavailable") from None
    return digest.hexdigest()


def validate_fixture_paths(backend_repo: Path, acceptance_fixture: Path):
    sandbox = backend_repo / "sandbox"
    realm = sandbox / "realm-export.json"
    source = sandbox / "specops/fixture/files/tinyGoApp.go"
    manifest = source.parents[1] / "manifest.json"
    required = (realm, source, manifest, acceptance_fixture)
    if not all(path.is_file() and not path.is_symlink() for path in required):
        raise FeedbackError("a required realm, source, manifest, or acceptance fixture is missing")
    for path in required[:3]:
        try:
            path.resolve(strict=True).relative_to(backend_repo.resolve(strict=True))
        except (OSError, ValueError):
            raise FeedbackError("backend fixture path escapes the supplied repository") from None
    try:
        manifest_data = json.loads(manifest.read_text(encoding="utf-8"))
        source_bytes = source.read_bytes()
        acceptance = acceptance_fixture.read_bytes()
    except (OSError, ValueError, UnicodeError):
        raise FeedbackError("a required fixture is unreadable") from None
    if (manifest_data.get("identity") != FIXTURE_REPO or manifest_data.get("commitSHA") != FIXTURE_COMMIT
            or hashlib.sha256(source_bytes).hexdigest() != FIXTURE_SOURCE_SHA256):
        raise FeedbackError("repository fixture identity or source checksum does not match the pinned facts")
    facts = (b"FR-01", b"INV-01", b"/healthz")
    if not all(marker in acceptance for marker in facts):
        raise FeedbackError("acceptance fixture lacks the explicit health endpoint facts")
    return {"realm": realm, "source": source, "manifest": manifest,
            "acceptance": acceptance_fixture, "sourceSha256": FIXTURE_SOURCE_SHA256}


def validate_docker_summary(raw: str):
    parts = raw.strip().split("\t")
    if len(parts) != 3 or parts != [DB_PROJECT, DB_SERVICE, DB_HOST_PORT]:
        raise FeedbackError("expected Docker project pyx-sandbox service db on host port 15432")
    return True


def verify_docker_db():
    docker_env = _safe_env(docker=True)
    ids = _run_fixed(["docker", "ps", "--filter", f"label=com.docker.compose.project={DB_PROJECT}",
                      "--filter", f"label=com.docker.compose.service={DB_SERVICE}", "--format", "{{.ID}}"],
                     timeout=8, env=docker_env).splitlines()
    if len(ids) != 1 or not ids[0] or any(ch not in "0123456789abcdef" for ch in ids[0].lower()):
        raise FeedbackError("expected exactly one running sandbox database container")
    template = ("{{index .Config.Labels \"com.docker.compose.project\"}}\t"
                "{{index .Config.Labels \"com.docker.compose.service\"}}\t"
                "{{range (index .NetworkSettings.Ports \"5432/tcp\")}}{{.HostPort}} {{end}}")
    summary = _run_fixed(["docker", "inspect", "--format", template, ids[0]], timeout=8, env=docker_env)
    ports = summary.split("\t")
    if len(ports) == 3:
        summary = "\t".join((ports[0], ports[1], " ".join(ports[2].split())))
    return validate_docker_summary(summary)


def verify_launcher(backend_repo: Path, runtime_dir: Path):
    launcher = backend_repo / "sandbox/specops/start-backend.py"
    if launcher.is_symlink() or not launcher.is_file():
        raise FeedbackError("versioned native backend status launcher is missing")
    output = _run_fixed([sys.executable, str(launcher), "status", "--runtime-dir", str(runtime_dir)],
                        timeout=7)
    try:
        status = json.loads(output)
    except json.JSONDecodeError:
        raise FeedbackError("native backend status metadata is invalid") from None
    validate_launcher_status(status)
    return {"status": "running", "port": 16080}


def validate_launcher_status(status):
    if (not isinstance(status, dict) or status.get("schemaVersion") != 1
            or status.get("status") != "running" or status.get("port") != 16080
            or status.get("endpoint") != API + "/readyz"):
        raise FeedbackError("the owned native backend is not running on the expected local port")
    return True


def verify_readyz():
    opener = build_opener(ProxyHandler({}))
    request = Request(API + "/readyz", headers={"User-Agent": "specops-feedback-check"})
    try:
        with opener.open(request, timeout=4) as response:
            code = response.status
    except (OSError, URLError):
        raise FeedbackError("backend readiness check failed") from None
    if code != 200:
        raise FeedbackError("backend readiness check failed")
    return True


def validate_cli(path: Path):
    supplied = Path(path).expanduser()
    if supplied.is_symlink():
        raise FeedbackError("CLI binary must not be a symlink")
    try:
        resolved = supplied.resolve(strict=True)
        info = resolved.stat()
    except OSError:
        raise FeedbackError("existing built CLI binary is unavailable") from None
    if not stat.S_ISREG(info.st_mode) or not os.access(resolved, os.X_OK):
        raise FeedbackError("CLI path must be an executable file")
    return resolved, sha256_file(resolved)


def validate_timeout(seconds):
    if not isinstance(seconds, int) or seconds < 60 or seconds > OVERALL_TIMEOUT_MAX:
        raise FeedbackError("walk timeout must be between 60 and 1800 seconds")
    return seconds


def preflight(args):
    started = time.monotonic()
    backend, backend_revision, backend_dirty = repo_revision(args.backend_repo)
    database, database_revision, database_dirty = repo_revision(args.database_repo)
    if not (database / "schema").is_dir() or not (database / "scripts").is_dir():
        raise FeedbackError("database checkout is missing its schema or scripts directories")
    acceptance = Path(__file__).resolve().parents[1] / "fixtures/specops-acceptance.md"
    fixtures = validate_fixture_paths(backend, acceptance)
    cli, cli_hash = validate_cli(args.cli)
    verify_docker_db()
    launcher = verify_launcher(backend, Path(args.runtime_dir).expanduser())
    verify_readyz()
    return {
        "backendRepo": backend, "databaseRepo": database, "cli": cli, "fixtures": fixtures,
        "revisions": {"backend": backend_revision, "database": database_revision,
                      "backendDirty": backend_dirty, "databaseDirty": database_dirty,
                      "cliSha256": cli_hash},
        "checks": {"dockerDatabase": "ready", "nativeBackend": launcher["status"], "readyz": "http_200"},
        "preflightSeconds": round(time.monotonic() - started, 3),
    }


def validate_runtime_for_evidence(runtime_dir: Path):
    try:
        info = runtime_dir.lstat()
    except OSError:
        raise FeedbackError("runtime directory is unavailable") from None
    if (not stat.S_ISDIR(info.st_mode) or stat.S_ISLNK(info.st_mode)
            or info.st_uid != os.getuid() or info.st_mode & 0o077):
        raise FeedbackError("runtime directory must be an owned private directory")


def write_evidence(runtime_dir: Path, record):
    runtime = Path(runtime_dir).expanduser()
    validate_runtime_for_evidence(runtime)
    directory = runtime / "feedback-evidence"
    if directory.is_symlink():
        raise FeedbackError("evidence directory symlink refused")
    try:
        directory.mkdir(mode=0o700, exist_ok=True)
        info = directory.lstat()
    except OSError:
        raise FeedbackError("could not prepare private evidence directory") from None
    if (not stat.S_ISDIR(info.st_mode) or info.st_uid != os.getuid() or info.st_mode & 0o077):
        raise FeedbackError("evidence directory must be an owned private directory")
    stamp = time.strftime("%Y%m%dT%H%M%SZ", time.gmtime())
    path = directory / f"feedback-{stamp}-{secrets.token_hex(4)}.json"
    payload = (json.dumps(record, sort_keys=True, separators=(",", ":")) + "\n").encode()
    try:
        fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        with os.fdopen(fd, "wb") as stream:
            stream.write(payload)
            stream.flush()
            os.fsync(stream.fileno())
        os.chmod(path, 0o600)
    except OSError:
        raise FeedbackError("could not write private feedback evidence") from None
    return path


def ensure_private_evidence_dir(runtime_dir: Path):
    runtime = Path(runtime_dir).expanduser()
    validate_runtime_for_evidence(runtime)
    parent = runtime / "feedback-evidence"
    leaf = parent / "children"
    for directory in (parent, leaf):
        if directory.is_symlink():
            raise FeedbackError("evidence directory symlink refused")
        try:
            directory.mkdir(mode=0o700, exist_ok=True)
            info = directory.lstat()
        except OSError:
            raise FeedbackError("could not prepare private evidence directory") from None
        if (not stat.S_ISDIR(info.st_mode) or info.st_uid != os.getuid() or info.st_mode & 0o077):
            raise FeedbackError("evidence directory must be an owned private directory")
    return leaf


def _kill_child_group(proc):
    try:
        os.killpg(proc.pid, signal.SIGTERM)
        proc.wait(timeout=2)
    except (OSError, subprocess.SubprocessError):
        try:
            os.killpg(proc.pid, signal.SIGKILL)
        except OSError:
            pass
        try:
            proc.wait(timeout=2)
        except subprocess.SubprocessError:
            pass


def run_child(argv, *, deadline, env=None):
    remaining = deadline - time.monotonic()
    if remaining <= 0:
        raise FeedbackError("overall walk deadline exceeded")
    try:
        proc = subprocess.Popen(argv, stdin=subprocess.DEVNULL, stdout=subprocess.PIPE,
                                stderr=subprocess.DEVNULL, env=env or _safe_env(),
                                start_new_session=True)
    except OSError:
        raise FeedbackError("workflow harness could not be started") from None
    try:
        stdout, _ = proc.communicate(timeout=remaining)
    except subprocess.TimeoutExpired:
        _kill_child_group(proc)
        raise FeedbackError("overall walk deadline exceeded") from None
    if proc.returncode != 0:
        raise FeedbackError("workflow harness failed")
    try:
        result = json.loads(stdout.decode("utf-8"))
    except (json.JSONDecodeError, UnicodeDecodeError):
        raise FeedbackError("workflow harness returned invalid compact JSON") from None
    if not isinstance(result, dict) or result.get("result") != "passed":
        raise FeedbackError("workflow harness result did not pass validation")
    return result


def run_walk(args, checks, *, started=None):
    started = time.monotonic() if started is None else started
    deadline = started + validate_timeout(args.max_runtime)
    script_dir = Path(__file__).resolve().parent
    cli_repo = script_dir.parent
    evidence_root = Path(args.runtime_dir).expanduser() / "feedback-evidence" / "children"
    phases = {"preflight": {"status": "passed", "seconds": checks["preflightSeconds"]}}
    record = {"schemaVersion": 1, "workflow": "specops-local-feedback", "providerMode": "local",
              "phases": phases, "revisions": checks["revisions"], "checks": checks["checks"]}
    error = None
    try:
        evidence_root = ensure_private_evidence_dir(Path(args.runtime_dir))
        common = ["--cli", str(checks["cli"]), "--backend", API, "--issuer", ISSUER,
                  "--realm-fixture", str(checks["fixtures"]["realm"]), "--evidence-dir", str(evidence_root)]
        child_env = _safe_env()
        m0_started = time.monotonic()
        m0_cmd = [sys.executable, str(script_dir / "specops-smoke.py"), *common,
                  "--project-name", "specops-local-feedback"]
        m0 = run_child(m0_cmd, deadline=deadline, env=child_env)
        if not isinstance(m0.get("projectId"), str) or not isinstance(m0.get("stage"), str):
            raise FeedbackError("M0 result omitted its compact project/status identity")
        phases["m0"] = {"status": "passed", "seconds": round(time.monotonic() - m0_started, 3),
                         "projectId": m0["projectId"], "stage": m0["stage"]}
        if args.to == "m1":
            m1_started = time.monotonic()
            acceptance = cli_repo / "fixtures/specops-acceptance.md"
            source = checks["fixtures"]["source"]
            m1_cmd = [sys.executable, str(script_dir / "specops-m1.py"), *common,
                      "--acceptance-fixture", str(acceptance), "--source-fixture", str(source),
                      "--project-name", "specops-local-fixture-walk", "--max-runtime",
                      str(max(60, min(900, int(deadline - time.monotonic()))))]
            m1 = run_child(m1_cmd, deadline=deadline, env=child_env)
            if (not isinstance(m1.get("projectId"), str) or not isinstance(m1.get("runId"), str)
                    or not isinstance(m1.get("compilationRevision"), int)):
                raise FeedbackError("M1 result omitted its compact workflow identity")
            phases["m1"] = {"status": "passed", "seconds": round(time.monotonic() - m1_started, 3),
                             "projectId": m1["projectId"], "runId": m1["runId"],
                             "compilationRevision": m1["compilationRevision"]}
        record["elapsedSeconds"] = round(time.monotonic() - started, 3)
        record["status"] = "passed"
    except Exception as exc:
        error = exc
        record["status"] = "failed"
        record["failureType"] = type(exc).__name__
        record["elapsedSeconds"] = round(time.monotonic() - started, 3)
    try:
        path = write_evidence(Path(args.runtime_dir).expanduser(), record)
    except FeedbackError:
        if error is None:
            raise
        path = None
    if error is not None:
        if isinstance(error, FeedbackError):
            raise error
        raise FeedbackError("local feedback walk failed") from None
    return {"result": "passed", "to": args.to, "evidence": str(path), "phases": phases}


def _add_common(parser):
    parser.add_argument("--backend-repo", type=Path, required=True)
    parser.add_argument("--database-repo", type=Path, required=True)
    parser.add_argument("--runtime-dir", type=Path, required=True)
    parser.add_argument("--cli", type=Path, required=True, help="existing built passo CLI executable")


def parser():
    ap = argparse.ArgumentParser(description="Check or run the local SpecOps feedback loop")
    sub = ap.add_subparsers(dest="action", required=True)
    check = sub.add_parser("check", help="read-only local prerequisite check")
    _add_common(check)
    walk = sub.add_parser("walk", help="run the public M0/M1 CLI feedback workflow")
    _add_common(walk)
    walk.add_argument("--to", required=True, help="supported targets: m0 or m1")
    walk.add_argument("--max-runtime", type=int, default=OVERALL_TIMEOUT_DEFAULT)
    return ap


def main(argv=None):
    args = parser().parse_args(argv)
    if args.action == "walk" and args.to not in {"m0", "m1"}:
        print("unsupported target; M2-M4 are not implemented", file=sys.stderr)
        return 20
    try:
        command_started = time.monotonic()
        if args.action == "walk":
            validate_timeout(args.max_runtime)
        try:
            checks = preflight(args)
        except FeedbackError as exc:
            if args.action == "walk":
                failed = {"schemaVersion": 1, "workflow": "specops-local-feedback", "providerMode": "local",
                          "status": "failed", "failureType": type(exc).__name__,
                          "phases": {"preflight": {"status": "failed",
                                                       "seconds": round(time.monotonic() - command_started, 3)}},
                          "elapsedSeconds": round(time.monotonic() - command_started, 3)}
                try:
                    evidence_path = write_evidence(Path(args.runtime_dir).expanduser(), failed)
                    failed["evidence"] = str(evidence_path)
                except FeedbackError:
                    pass
            raise
        if args.action == "check":
            print(json.dumps({"result": "ready", "revisions": checks["revisions"], "checks": checks["checks"]},
                             sort_keys=True))
            return 0
        result = run_walk(args, checks, started=command_started)
        print(json.dumps(result, sort_keys=True))
        return 0
    except Exception as exc:
        message = str(exc) if isinstance(exc, FeedbackError) else type(exc).__name__
        print("feedback failed: " + message, file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
