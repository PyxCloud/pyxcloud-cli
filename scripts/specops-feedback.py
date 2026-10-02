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
import uuid
from urllib.error import URLError
from urllib.request import ProxyHandler, Request, build_opener


API = "http://127.0.0.1:16080"
ISSUER = "http://sso.localtest.me:18081/realms/passobuild"
DB_PROJECT = "pyx-sandbox"
DB_SERVICE = "db"
DB_HOST_PORT = "15432"
DB_HOST_IP = "127.0.0.1"
SAFE_NETWORK = "pyx-sandbox-specops-internal"
SSO_SERVICE = "sso"
SSO_FORWARDER_SERVICE = "expose-sso"
SSO_REALM_EXPORT_TARGET = "/opt/keycloak/data/import/realm-export.json"
SSO_H2_TARGET = "/opt/keycloak/data/h2"
SAFE_REALM_NAME = "passobuild"
SAFE_REALM_USER_NAMESPACE = uuid.UUID("6419a65a-852e-5d5c-9f3f-86e56590b3d5")
GUARD_NAME = "macos-sandbox-exec"
GUARD_VERSION = 1
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


def verify_local_docker_context():
    if os.environ.get("DOCKER_HOST") or os.environ.get("DOCKER_TLS_VERIFY"):
        raise FeedbackError("certified feedback requires the local Docker context")
    docker_env = _safe_env(docker=True)
    context = _run_fixed(["docker", "context", "show"], timeout=5, env=docker_env)
    if not context or any(ch not in "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_.-" for ch in context):
        raise FeedbackError("Docker context identity is invalid")
    endpoint = _run_fixed(["docker", "context", "inspect", "--format",
                           "{{.Endpoints.docker.Host}}", context], timeout=5, env=docker_env)
    if not endpoint.startswith("unix://"):
        raise FeedbackError("certified feedback requires a local Unix-socket Docker daemon")
    return True


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


HARNESS_SOURCES = (
    "scripts/specops-feedback.py",
    "scripts/specops-smoke.py",
    "scripts/specops-m1.py",
    "fixtures/specops-acceptance.md",
)


def validate_harness_sources(cli_repo: Path, executed_entrypoint=None):
    root = Path(cli_repo).resolve(strict=True)
    hashes = {}
    paths = {}
    for relative in HARNESS_SOURCES:
        path = root / relative
        if path.is_symlink() or not path.is_file():
            raise FeedbackError("a required versioned workflow source is missing or a symlink")
        resolved = path.resolve(strict=True)
        try:
            resolved.relative_to(root)
        except ValueError:
            raise FeedbackError("a workflow source escapes the explicit CLI repository") from None
        tracked = _run_fixed(["git", "-C", str(root), "ls-files", "--error-unmatch", "--", relative], timeout=5)
        if tracked != relative:
            raise FeedbackError("a workflow source is not tracked by the CLI repository")
        changed = _run_fixed(["git", "-C", str(root), "status", "--porcelain=v1", "--untracked-files=no", "--", relative], timeout=5)
        if changed:
            raise FeedbackError("tracked workflow sources must be clean for a certified walk")
        hashes[relative] = sha256_file(resolved)
        paths[relative] = resolved
    entrypoint = Path(executed_entrypoint or __file__).resolve()
    entrypoint = entrypoint.resolve(strict=True)
    if entrypoint != paths[HARNESS_SOURCES[0]]:
        raise FeedbackError("executed feedback entrypoint differs from the explicit CLI checkout")
    return {"paths": paths, "sha256": hashes}


def validate_tracked_file(repository: Path, relative: str):
    root = Path(repository).resolve(strict=True)
    path = root / relative
    if path.is_symlink() or not path.is_file():
        raise FeedbackError("required versioned launcher source is missing or a symlink")
    try:
        path.resolve(strict=True).relative_to(root)
    except ValueError:
        raise FeedbackError("versioned launcher source escapes its repository") from None
    if _run_fixed(["git", "-C", str(root), "ls-files", "--error-unmatch", "--", relative], timeout=5) != relative:
        raise FeedbackError("required launcher source is not tracked")
    if _run_fixed(["git", "-C", str(root), "status", "--porcelain=v1", "--untracked-files=no", "--", relative], timeout=5):
        raise FeedbackError("tracked launcher source must be clean for a certified walk")
    return path.resolve(strict=True), sha256_file(path)


def validate_safe_realm_fixture(backend_repo: Path, runtime_dir: Path):
    source = backend_repo / "sandbox/realm-export.json"
    safe_export = runtime_dir / "realm-export.safe.json"
    try:
        source_info = source.lstat()
        export_info = safe_export.lstat()
        runtime_info = runtime_dir.lstat()
        if (not stat.S_ISREG(source_info.st_mode) or stat.S_ISLNK(source_info.st_mode)
                or not stat.S_ISREG(export_info.st_mode) or stat.S_ISLNK(export_info.st_mode)
                or export_info.st_uid != os.getuid() or stat.S_IMODE(export_info.st_mode) != 0o600
                or runtime_info.st_uid != os.getuid() or stat.S_IMODE(runtime_info.st_mode) != 0o700):
            raise FeedbackError("private stable-ID SSO realm fixture is unavailable")
        original = json.loads(source.read_text(encoding="utf-8"))
        exported = json.loads(safe_export.read_text(encoding="utf-8"))
    except FeedbackError:
        raise
    except (OSError, ValueError, UnicodeError):
        raise FeedbackError("private stable-ID SSO realm fixture is unavailable") from None
    if not isinstance(original, dict) or original.get("realm") != SAFE_REALM_NAME or not isinstance(exported, dict):
        raise FeedbackError("safe SSO realm identity is invalid")
    expected = json.loads(json.dumps(original))
    users = expected.get("users")
    if not isinstance(users, list) or not users:
        raise FeedbackError("safe SSO realm has no stable fixture users")
    seen = set()
    for user in users:
        if not isinstance(user, dict) or not isinstance(user.get("username"), str):
            raise FeedbackError("safe SSO realm user identity is invalid")
        username = user["username"].strip().lower()
        if not username or username in seen:
            raise FeedbackError("safe SSO realm user identity is ambiguous")
        seen.add(username)
        user["id"] = str(uuid.uuid5(SAFE_REALM_USER_NAMESPACE, f"{SAFE_REALM_NAME}/{username}"))
    if exported != expected:
        raise FeedbackError("safe SSO realm fixture does not preserve stable user identities")
    return safe_export


def validate_docker_summary(project, service, port_bindings, networks):
    if project != DB_PROJECT or service != DB_SERVICE:
        raise FeedbackError("expected Docker project pyx-sandbox service db")
    if port_bindings != [{"HostIp": DB_HOST_IP, "HostPort": DB_HOST_PORT}]:
        raise FeedbackError("database host binding must be exactly 127.0.0.1:15432")
    if not isinstance(networks, dict) or set(networks) != {SAFE_NETWORK}:
        raise FeedbackError("database must use only the approved internal SpecOps network")
    return True


def verify_docker_network():
    raw = _run_fixed(["docker", "network", "inspect", "--format", "{{json .Internal}}", SAFE_NETWORK],
                     timeout=8, env=_safe_env(docker=True))
    if raw != "true":
        raise FeedbackError("approved SpecOps Docker network must be internal")
    return True


def verify_docker_db():
    verify_local_docker_context()
    docker_env = _safe_env(docker=True)
    ids = _run_fixed(["docker", "ps", "--filter", f"label=com.docker.compose.project={DB_PROJECT}",
                      "--filter", f"label=com.docker.compose.service={DB_SERVICE}", "--format", "{{.ID}}"],
                     timeout=8, env=docker_env).splitlines()
    if len(ids) != 1 or not ids[0] or any(ch not in "0123456789abcdef" for ch in ids[0].lower()):
        raise FeedbackError("expected exactly one running sandbox database container")
    template = ("{{index .Config.Labels \"com.docker.compose.project\"}}\t"
                "{{index .Config.Labels \"com.docker.compose.service\"}}\t"
                "{{json (index .HostConfig.PortBindings \"5432/tcp\")}}\t"
                "{{json .NetworkSettings.Networks}}")
    summary = _run_fixed(["docker", "inspect", "--format", template, ids[0]], timeout=8, env=docker_env)
    try:
        project, service, ports_raw, networks_raw = summary.split("\t", 3)
        ports, networks = json.loads(ports_raw), json.loads(networks_raw)
    except (ValueError, json.JSONDecodeError):
        raise FeedbackError("database Docker metadata is invalid") from None
    validate_docker_summary(project, service, ports, networks)
    verify_docker_network()
    return True


def validate_docker_sso_summary(project, service, ports, networks, mounts, expected_realm, expected_volume):
    if project != DB_PROJECT or service != SSO_SERVICE:
        raise FeedbackError("expected the owned pyx-sandbox SSO service")
    if not isinstance(ports, dict) or any(bindings for bindings in ports.values() if bindings):
        raise FeedbackError("Keycloak must not publish container ports directly")
    if not isinstance(networks, dict) or set(networks) != {SAFE_NETWORK}:
        raise FeedbackError("Keycloak must use only the approved internal SpecOps network")
    if not isinstance(mounts, list):
        raise FeedbackError("Keycloak mount metadata is invalid")
    realm_mounts = [m for m in mounts if isinstance(m, dict) and m.get("Destination") == SSO_REALM_EXPORT_TARGET]
    h2_mounts = [m for m in mounts if isinstance(m, dict) and m.get("Destination") == SSO_H2_TARGET]
    if (len(realm_mounts) != 1 or realm_mounts[0].get("Type") != "bind"
            or Path(str(realm_mounts[0].get("Source", ""))).resolve() != expected_realm.resolve()):
        raise FeedbackError("Keycloak is not importing the private stable-ID realm fixture")
    if (len(h2_mounts) != 1 or h2_mounts[0].get("Type") != "volume"
            or h2_mounts[0].get("Name") != expected_volume
            or not isinstance(h2_mounts[0].get("Source"), str) or not h2_mounts[0]["Source"]):
        raise FeedbackError("Keycloak H2 data must use the persistent named SpecOps volume")
    return True


def expected_sso_volume(backend_repo: Path):
    root = Path(backend_repo).resolve(strict=True)
    digest = hashlib.sha1(str(root).encode("utf-8")).hexdigest()[:8]
    return f"pyx-sandbox-specops-sso-{digest}"


def verify_docker_sso(backend_repo: Path, runtime_dir: Path):
    docker_env = _safe_env(docker=True)
    ids = _run_fixed(["docker", "ps", "--filter", f"label=com.docker.compose.project={DB_PROJECT}",
                      "--filter", f"label=com.docker.compose.service={SSO_SERVICE}", "--format", "{{.ID}}"],
                     timeout=8, env=docker_env).splitlines()
    if len(ids) != 1 or not ids[0] or any(ch not in "0123456789abcdef" for ch in ids[0].lower()):
        raise FeedbackError("expected exactly one running sandbox SSO container")
    template = ("{{index .Config.Labels \"com.docker.compose.project\"}}\t"
                "{{index .Config.Labels \"com.docker.compose.service\"}}\t"
                "{{json .NetworkSettings.Ports}}\t{{json .NetworkSettings.Networks}}\t{{json .Mounts}}")
    summary = _run_fixed(["docker", "inspect", "--format", template, ids[0]], timeout=8, env=docker_env)
    try:
        project, service, ports_raw, networks_raw, mounts_raw = summary.split("\t", 4)
        ports, networks, mounts = json.loads(ports_raw), json.loads(networks_raw), json.loads(mounts_raw)
    except (ValueError, json.JSONDecodeError):
        raise FeedbackError("SSO Docker metadata is invalid") from None
    realm = runtime_dir / "realm-export.safe.json"
    validate_docker_sso_summary(project, service, ports, networks, mounts, realm,
                                expected_sso_volume(backend_repo))
    return True


def validate_docker_forwarder_summary(project, service, port_bindings, networks):
    if project != DB_PROJECT or service != SSO_FORWARDER_SERVICE:
        raise FeedbackError("expected the owned pyx-sandbox SSO forwarder")
    if port_bindings != [{"HostIp": DB_HOST_IP, "HostPort": "18081"}]:
        raise FeedbackError("SSO forwarder host binding must be exactly 127.0.0.1:18081")
    if not isinstance(networks, dict) or set(networks) != {SAFE_NETWORK}:
        raise FeedbackError("SSO forwarder must use only the approved internal SpecOps network")
    verify_docker_network()
    return True


def verify_docker_forwarder():
    docker_env = _safe_env(docker=True)
    ids = _run_fixed(["docker", "ps", "--filter", f"label=com.docker.compose.project={DB_PROJECT}",
                      "--filter", f"label=com.docker.compose.service={SSO_FORWARDER_SERVICE}",
                      "--format", "{{.ID}}"], timeout=8, env=docker_env).splitlines()
    if len(ids) != 1 or not ids[0] or any(ch not in "0123456789abcdef" for ch in ids[0].lower()):
        raise FeedbackError("expected exactly one running sandbox SSO forwarder")
    template = ("{{index .Config.Labels \"com.docker.compose.project\"}}\t"
                "{{index .Config.Labels \"com.docker.compose.service\"}}\t"
                "{{json (index .HostConfig.PortBindings \"18081/tcp\")}}\t"
                "{{json .NetworkSettings.Networks}}")
    summary = _run_fixed(["docker", "inspect", "--format", template, ids[0]], timeout=8, env=docker_env)
    try:
        project, service, ports_raw, networks_raw = summary.split("\t", 3)
        ports, networks = json.loads(ports_raw), json.loads(networks_raw)
    except (ValueError, json.JSONDecodeError):
        raise FeedbackError("SSO forwarder Docker metadata is invalid") from None
    validate_docker_forwarder_summary(project, service, ports, networks)
    return True


_container_guard_cache = {}

def verify_launcher(backend_repo: Path, runtime_dir: Path):
    launcher, launcher_sha = validate_tracked_file(backend_repo, "sandbox/specops/start-backend.py")
    output = _run_fixed([sys.executable, str(launcher), "status", "--runtime-dir", str(runtime_dir)],
                        timeout=40)
    try:
        status = json.loads(output)
    except json.JSONDecodeError:
        raise FeedbackError("native backend status metadata is invalid") from None
    validate_launcher_status(status)
    if status.get("guard") == {"name": "docker-internal-network", "version": 1, "selfTest": "passed"}:
        _container_guard_cache[str(Path(runtime_dir).resolve())] = status["guard"]
    guard = read_launcher_guard(backend_repo, Path(runtime_dir))
    return {"status": "running", "port": 16080, "guard": guard,
            "launcher": launcher, "launcherSha256": launcher_sha}


def validate_launcher_status(status):
    if (not isinstance(status, dict) or status.get("schemaVersion") != 1
            or status.get("status") != "running" or status.get("port") != 16080
            or status.get("endpoint") != API + "/readyz"):
        raise FeedbackError("the owned native backend is not running on the expected local port")
    return True


def validate_launcher_guard(metadata, profile_bytes, runtime_dir):
    guard = metadata.get("guard") if isinstance(metadata, dict) else None
    expected_hash = hashlib.sha256(profile_bytes).hexdigest()
    if (not isinstance(metadata, dict) or metadata.get("schemaVersion") != 1
            or metadata.get("status") != "running" or metadata.get("port") != 16080
            or Path(str(metadata.get("runtimeDir", ""))).resolve() != Path(runtime_dir).resolve()
            or not isinstance(guard, dict) or guard.get("name") != GUARD_NAME
            or guard.get("version") != GUARD_VERSION or guard.get("profileSha256") != expected_hash
            or guard.get("selfTest") != "passed"):
        raise FeedbackError("owned backend OS guard metadata or verified policy hash is invalid")
    return {"name": GUARD_NAME, "version": GUARD_VERSION, "profileSha256": expected_hash}


def read_launcher_guard(backend_repo: Path, runtime_dir: Path):
    if (runtime_dir / "container.json").is_file():
        launcher, _ = validate_tracked_file(backend_repo, "sandbox/specops/container-sandbox.py")
        cached = _container_guard_cache.get(str(runtime_dir.resolve()))
        if cached is not None:
            return dict(cached)
        result = json.loads(_run_fixed([sys.executable, str(launcher), "status", "--runtime-dir", str(runtime_dir)], timeout=40))
        validate_launcher_status(result)
        guard = result.get("guard", {})
        if guard != {"name": "docker-internal-network", "version": 1, "selfTest": "passed"}:
            raise FeedbackError("container isolation proof failed")
        return guard
    if sys.platform != "darwin":
        raise FeedbackError("certified feedback requires the macOS kernel sandbox guard")
    metadata_path = runtime_dir / "backend.json"
    profile_path = runtime_dir / "sandbox.sb"
    try:
        for path in (metadata_path, profile_path):
            info = path.lstat()
            if (not stat.S_ISREG(info.st_mode) or stat.S_ISLNK(info.st_mode)
                    or info.st_uid != os.getuid() or stat.S_IMODE(info.st_mode) != 0o600):
                raise FeedbackError("owned backend OS guard files are not private regular files")
        metadata = json.loads(metadata_path.read_text(encoding="utf-8"))
        profile = profile_path.read_bytes()
    except FeedbackError:
        raise
    except (OSError, ValueError, UnicodeError):
        raise FeedbackError("owned backend OS guard metadata is unavailable") from None
    return validate_launcher_guard(metadata, profile, runtime_dir)


def validate_feedback_role(metadata, profile_bytes, *, runtime_dir, cli_repo, python_prefix, cli_binary, fixture_files=()):
    roles = metadata.get("roles") if isinstance(metadata, dict) else None
    role = roles.get("feedback-client") if isinstance(roles, dict) else None
    expected_hash = hashlib.sha256(profile_bytes).hexdigest()
    expected_reads = [str(Path(cli_repo).resolve()), str(Path(python_prefix).resolve())]
    expected_files = [str(Path(cli_binary).resolve()), *(str(Path(p).resolve()) for p in fixture_files)]
    if (not isinstance(role, dict) or role.get("profileFile") != "feedback-client.sb"
            or role.get("profileSha256") != expected_hash or role.get("selfTest") != "passed"
            or role.get("readDirectories") != expected_reads or role.get("readFiles") != expected_files
            or role.get("outbound") != ["127.0.0.1:16080", "127.0.0.1:18081", "127.0.0.1:18089"]
            or role.get("inbound") != []):
        raise FeedbackError("feedback-client guard metadata or verified policy hash is invalid")
    return {"role": "feedback-client", "profileSha256": expected_hash,
            "selfTest": "passed", "outbound": role["outbound"], "inbound": []}


def read_feedback_role(runtime_dir, cli_repo, python_prefix, cli_binary, fixture_files=()):
    runtime = Path(runtime_dir)
    metadata_path = runtime / "backend.json"
    profile_path = runtime / "feedback-client.sb"
    try:
        for path in (metadata_path, profile_path):
            info = path.lstat()
            if (not stat.S_ISREG(info.st_mode) or stat.S_ISLNK(info.st_mode)
                    or info.st_uid != os.getuid() or stat.S_IMODE(info.st_mode) != 0o600):
                raise FeedbackError("feedback-client guard files are not private regular files")
        metadata = json.loads(metadata_path.read_text(encoding="utf-8"))
        profile = profile_path.read_bytes()
    except FeedbackError:
        raise
    except (OSError, ValueError, UnicodeError):
        raise FeedbackError("feedback-client guard metadata is unavailable") from None
    return validate_feedback_role(metadata, profile, runtime_dir=runtime_dir, cli_repo=cli_repo,
                                  python_prefix=python_prefix, cli_binary=cli_binary, fixture_files=fixture_files)


def prepare_feedback_role(checks, *, timeout=20):
    python_prefix = Path(sys.prefix).resolve(strict=True)
    launcher = checks["launcher"]
    python = Path(sys.executable).resolve(strict=True)
    command = [sys.executable, str(launcher), "guarded-exec", "--role", "feedback",
               "--runtime-dir", str(checks["runtimeDir"]), "--read-dir", str(checks["cliRepo"]),
               "--read-dir", str(python_prefix), "--executable", str(python),
               "--read-file", str(checks["cli"]), "--validate-only"]
    files = [checks["fixtures"][key] for key in ("realm", "source", "manifest")]
    command[-1:-1] = [arg for p in files for arg in ("--read-file", str(p))]
    _run_fixed(command, timeout=timeout, env=_safe_env())
    role = read_feedback_role(checks["runtimeDir"], checks["cliRepo"], python_prefix, checks["cli"], files)
    checks["checks"]["feedbackClientGuard"] = role
    checks["feedbackPythonPrefix"] = python_prefix
    return role


def verify_readyz():
    opener = build_opener(ProxyHandler({}))
    request = Request(API + "/readyz", headers={"User-Agent": "specops-feedback-check"})
    try:
        with opener.open(request, timeout=15) as response:
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
    cli_repo, cli_revision, cli_dirty = repo_revision(Path(__file__).resolve().parents[1])
    if not (database / "schema").is_dir() or not (database / "scripts").is_dir():
        raise FeedbackError("database checkout is missing its schema or scripts directories")
    source_provenance = validate_harness_sources(cli_repo)
    acceptance = source_provenance["paths"]["fixtures/specops-acceptance.md"]
    fixtures = validate_fixture_paths(backend, acceptance)
    runtime_dir = Path(args.runtime_dir).expanduser()
    validate_runtime_for_evidence(runtime_dir)
    safe_realm = validate_safe_realm_fixture(backend, runtime_dir)
    cli, cli_hash = validate_cli(args.cli)
    verify_docker_db()
    verify_docker_sso(backend, runtime_dir)
    verify_docker_forwarder()
    launcher = verify_launcher(backend, runtime_dir)
    verify_readyz()
    return {
        "backendRepo": backend, "databaseRepo": database, "cliRepo": cli_repo, "cli": cli,
        "runtimeDir": runtime_dir, "fixtures": fixtures, "safeRealm": safe_realm,
        "sourceProvenance": source_provenance,
        "launcher": launcher["launcher"], "launcherSha256": launcher["launcherSha256"],
        "revisions": {"backend": backend_revision, "database": database_revision,
                      "backendDirty": backend_dirty, "databaseDirty": database_dirty,
                      "cli": cli_revision, "cliTrackedDirty": cli_dirty, "cliBinarySha256": cli_hash,
                      "workflowSourceSha256": source_provenance["sha256"]},
        "checks": {"dockerDatabase": "ready_loopback", "dockerNetwork": SAFE_NETWORK,
                   "dockerSso": "stable_realm_and_volume", "ssoForwarder": "ready_loopback",
                   "nativeBackend": launcher["status"],
                   "backendGuard": launcher["guard"], "readyz": "http_200"},
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


def assert_execution_provenance(checks):
    current = validate_harness_sources(checks["cliRepo"])
    if current["sha256"] != checks["sourceProvenance"]["sha256"]:
        raise FeedbackError("versioned workflow sources changed after preflight")
    cli, digest = validate_cli(checks["cli"])
    if cli != checks["cli"] or digest != checks["revisions"]["cliBinarySha256"]:
        raise FeedbackError("built CLI binary changed after preflight")
    launcher, launcher_sha = validate_tracked_file(checks["backendRepo"], "sandbox/specops/start-backend.py")
    if launcher != checks["launcher"] or launcher_sha != checks["launcherSha256"]:
        raise FeedbackError("versioned native guard launcher changed after preflight")
    guard = read_launcher_guard(checks["backendRepo"], checks["runtimeDir"])
    if guard != checks["checks"]["backendGuard"]:
        raise FeedbackError("owned backend OS guard policy changed after preflight")
    if "feedbackClientGuard" in checks["checks"]:
        client = read_feedback_role(checks["runtimeDir"], checks["cliRepo"],
                                    checks["feedbackPythonPrefix"], checks["cli"],
                                    [checks["fixtures"][key] for key in ("realm", "source", "manifest")])
        if client != checks["checks"]["feedbackClientGuard"]:
            raise FeedbackError("feedback-client OS guard policy changed after preflight")


def guarded_child_argv(checks, script_path, argv):
    if sys.platform != "darwin":
        raise FeedbackError("certified workflow requires the verified macOS feedback-client guard")
    launcher = checks["launcher"]
    python = Path(sys.executable).resolve(strict=True)
    script_path = Path(script_path).resolve(strict=True)
    cli = checks["cli"].resolve(strict=True)
    expected_args = [str(python), str(script_path), *argv[2:]]
    if argv[:2] != [sys.executable, str(script_path)]:
        raise FeedbackError("workflow child argv differs from the validated interpreter and source path")
    home = Path.home().resolve()
    prefix = Path(sys.prefix).resolve(strict=True)
    try:
        prefix.relative_to(home)
    except ValueError:
        pass
    else:
        raise FeedbackError("certified Python environment must be outside the home directory")
    shim = (
        "import runpy,socket,sys; "
        "script=sys.argv[1]; args=sys.argv[2:]; original=socket.getaddrinfo; "
        "socket.getaddrinfo=lambda host,port,*a,**k: original('127.0.0.1' if host=='sso.localtest.me' and str(port)=='18081' else host,port,*a,**k); "
        "sys.argv=[script,*args]; runpy.run_path(script,run_name='__main__')"
    )
    child_argv = [str(python), "-c", shim, str(script_path), *expected_args[2:]]
    guarded = [sys.executable, str(launcher), "guarded-exec", "--role", "feedback",
               "--runtime-dir", str(Path(checks["runtimeDir"]).resolve()),
               "--read-dir", str(checks["cliRepo"].resolve()), "--read-dir", str(prefix),
               "--executable", str(python), "--read-file", str(cli), "--"]
    guarded[-1:-1] = [arg for p in [checks["fixtures"][key] for key in ("realm", "source", "manifest")] for arg in ("--read-file", str(p))]
    return [*guarded, *child_argv]


def _private_json_file(path, parent):
    path = Path(path)
    if path.is_symlink() or path.resolve().parent != Path(parent).resolve():
        raise FeedbackError("workflow evidence path escapes its private directory")
    try:
        info = path.lstat()
        if not stat.S_ISREG(info.st_mode) or info.st_uid != os.getuid() or stat.S_IMODE(info.st_mode) != 0o600:
            raise FeedbackError("workflow evidence is not a private owned regular file")
        return json.loads(path.read_text(encoding="utf-8"))
    except FeedbackError:
        raise
    except (OSError, ValueError, UnicodeError):
        raise FeedbackError("workflow evidence is invalid or unavailable") from None


def validate_child_evidence(envelope, *, phase, evidence_root, project_id, run_id=None,
                            compilation_revision=None, source_sha256=None, acceptance_sha256=None):
    evidence = envelope.get("evidence") if isinstance(envelope, dict) else None
    if not isinstance(evidence, str):
        raise FeedbackError("workflow child omitted its compact evidence path")
    evidence_path = Path(evidence)
    expected_name = "m0-smoke.json" if phase == "m0" else "m1-specops.json"
    if evidence_path.name != expected_name:
        raise FeedbackError("workflow evidence filename does not match the invoked phase")
    record = _private_json_file(evidence_path, evidence_root)
    if phase == "m0":
        if (record.get("providerMode") != "local" or record.get("authMode") != "browser-pkce"
                or record.get("backend") not in {API, "127.0.0.1:16080"} or str(record.get("projectId")) != project_id
                or record.get("projectName") != "specops-local-feedback"
                or record.get("readCount") != 2 or record.get("secondReadNoMutation") is not True
                or record.get("first") != record.get("second")
                or not isinstance(record.get("first"), dict)
                or not isinstance(record["first"].get("stage"), str)
                or record["first"].get("stage") != envelope.get("stage")
                or record["first"].get("nextActionKey") != envelope.get("nextActionKey")):
            raise FeedbackError("M0 public status evidence did not verify two identical canonical reads")
    elif phase == "m1":
        outcomes = record.get("outcomes") if isinstance(record, dict) else None
        if (record.get("workflow") != "M1-local-specops-fixture-walk" or record.get("providerMode") != "local"
                or record.get("authMode") != "browser-pkce" or str(record.get("projectId")) != project_id
                or record.get("projectName") != "specops-local-fixture-walk"
                or record.get("fixtureSourceSha256") != source_sha256
                or record.get("sourceLabel") != "fixture_source_not_repository_proof"
                or not isinstance(outcomes, dict) or outcomes.get("discoveryStatus") != "succeeded"
                or outcomes.get("discoveryRunId") != run_id
                or outcomes.get("compilationRevision") != compilation_revision
                or outcomes.get("repeatCompileSameIdentity") is not True
                or type(outcomes.get("docsGenerated")) is not bool
                or outcomes.get("defineApply") != "completed"
                or outcomes.get("canonicalScopeStateVerified") is not True
                or outcomes.get("journeyRead") != "verified_project_scope"
                or outcomes.get("fixtureSourceLabel") != "fixture_source_not_repository_proof"
                or outcomes.get("fixtureSourceSha256") != source_sha256
                or outcomes.get("fixtureRepositoryCommit") != FIXTURE_COMMIT
                or outcomes.get("fixtureDocumentSourceLabel") != "user_acceptance_fixture_not_repository_proof"
                or outcomes.get("fixtureContentSha256") != acceptance_sha256
                or outcomes.get("fixtureDocumentFileName") != f"specops-acceptance-{acceptance_sha256[:16]}.md"
                or type(outcomes.get("fixtureDocumentSizeBytes")) is not int
                or outcomes.get("fixtureDocumentSizeBytes") <= 0
                or outcomes.get("fixtureDocument") not in {
                    "reused_unique_ready_filename_size_match",
                    "uploaded_once_and_verified_unique_ready_filename_size_match",
                }
                or not isinstance(outcomes.get("documentId"), str) or not outcomes.get("documentId")
                or not isinstance(outcomes.get("compilationId"), str) or not outcomes.get("compilationId")):
            raise FeedbackError("M1 evidence did not verify the public discovery/docs/compile/scope state")
    else:
        raise FeedbackError("unrecognized workflow phase evidence")
    return {"path": str(evidence_path.resolve()), "sha256": sha256_file(evidence_path),
            "publicStateVerified": True}


def run_walk(args, checks, *, started=None):
    started = time.monotonic() if started is None else started
    deadline = started + validate_timeout(args.max_runtime)
    evidence_root = Path(args.runtime_dir).expanduser() / "feedback-evidence" / "children"
    phases = {"preflight": {"status": "passed", "seconds": checks["preflightSeconds"]}}
    record = {"schemaVersion": 1, "workflow": "specops-local-feedback", "providerMode": "local",
              "phases": phases, "revisions": checks["revisions"], "checks": checks["checks"],
              "sources": {"entrypoint": str(checks["sourceProvenance"]["paths"][HARNESS_SOURCES[0]]),
                          "workflowPaths": {key: str(value) for key, value in checks["sourceProvenance"]["paths"].items()},
                          "workflowSha256": checks["sourceProvenance"]["sha256"],
                          "guardLauncher": str(checks["launcher"]),
                          "guardLauncherSha256": checks["launcherSha256"],
                          "cliBinary": str(checks["cli"]),
                          "cliBinarySha256": checks["revisions"]["cliBinarySha256"]},
              "guard": checks["checks"]["backendGuard"]}
    error = None
    try:
        evidence_root = ensure_private_evidence_dir(Path(args.runtime_dir))
        prepare_feedback_role(checks)
        assert_execution_provenance(checks)
        record["clientGuard"] = checks["checks"]["feedbackClientGuard"]
        common = ["--cli", str(checks["cli"]), "--backend", API, "--issuer", ISSUER,
                  "--realm-fixture", str(checks["fixtures"]["realm"]), "--evidence-dir", str(evidence_root)]
        child_env = _safe_env()
        m0_started = time.monotonic()
        m0_path = checks["sourceProvenance"]["paths"]["scripts/specops-smoke.py"]
        m0_cmd = [sys.executable, str(m0_path), *common,
                  "--project-name", "specops-local-feedback"]
        assert_execution_provenance(checks)
        m0 = run_child(guarded_child_argv(checks, m0_path, m0_cmd), deadline=deadline, env=child_env)
        assert_execution_provenance(checks)
        if not isinstance(m0.get("projectId"), str) or not isinstance(m0.get("stage"), str):
            raise FeedbackError("M0 result omitted its compact project/status identity")
        m0_evidence = validate_child_evidence(m0, phase="m0", evidence_root=evidence_root,
                                               project_id=m0["projectId"])
        phases["m0"] = {"status": "passed", "seconds": round(time.monotonic() - m0_started, 3),
                         "projectId": m0["projectId"], "stage": m0["stage"],
                         "publicStateVerified": m0_evidence["publicStateVerified"],
                         "evidenceSha256": m0_evidence["sha256"]}
        if args.to == "m1":
            m1_started = time.monotonic()
            acceptance = checks["sourceProvenance"]["paths"]["fixtures/specops-acceptance.md"]
            source = checks["fixtures"]["source"]
            m1_path = checks["sourceProvenance"]["paths"]["scripts/specops-m1.py"]
            m1_cmd = [sys.executable, str(m1_path), *common,
                      "--acceptance-fixture", str(acceptance), "--source-fixture", str(source),
                      "--project-name", "specops-local-fixture-walk", "--max-runtime",
                      str(max(60, min(900, int(deadline - time.monotonic()))))]
            assert_execution_provenance(checks)
            m1 = run_child(guarded_child_argv(checks, m1_path, m1_cmd), deadline=deadline, env=child_env)
            assert_execution_provenance(checks)
            if (not isinstance(m1.get("projectId"), str) or not isinstance(m1.get("runId"), str)
                    or not isinstance(m1.get("compilationRevision"), int)):
                raise FeedbackError("M1 result omitted its compact workflow identity")
            m1_evidence = validate_child_evidence(
                m1, phase="m1", evidence_root=evidence_root, project_id=m1["projectId"],
                run_id=m1["runId"], compilation_revision=m1["compilationRevision"],
                source_sha256=checks["fixtures"]["sourceSha256"],
                acceptance_sha256=sha256_file(acceptance))
            phases["m1"] = {"status": "passed", "seconds": round(time.monotonic() - m1_started, 3),
                             "projectId": m1["projectId"], "runId": m1["runId"],
                             "compilationRevision": m1["compilationRevision"],
                             "publicStateVerified": m1_evidence["publicStateVerified"],
                             "evidenceSha256": m1_evidence["sha256"]}
        if checks["checks"]["backendGuard"].get("name") == "docker-internal-network":
            verify_launcher(checks["backendRepo"], checks["runtimeDir"])
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
