#!/usr/bin/env python3
"""Freeze a database produced by a checksummed, published Linux amd64 release."""

import argparse
import hashlib
import json
from pathlib import Path
import platform
import re
import shutil
import sqlite3
import subprocess
import tempfile

REPOSITORY = "RandomCodeSpace/kb"
SECRET = "synthetic-migration-fixture-secret-only"
ROOT = Path(__file__).resolve().parents[1] / "internal/store/testdata/migrations"


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("version", help="published release tag, such as v1.10.0")
    parser.add_argument("sha256", help="independently checked kb-linux-amd64 asset SHA-256")
    args = parser.parse_args()
    if not re.fullmatch(r"v[0-9]+\.[0-9]+\.[0-9]+", args.version):
        parser.error("version must be a release tag vMAJOR.MINOR.PATCH")
    if not re.fullmatch(r"[0-9a-f]{64}", args.sha256):
        parser.error("sha256 must contain 64 lowercase hexadecimal digits")
    if platform.system() != "Linux" or platform.machine() not in ("x86_64", "AMD64"):
        parser.error("generation requires Linux amd64; fixture tests are portable")
    destination = ROOT / args.version
    if destination.exists():
        parser.error(f"refusing to replace frozen fixture {destination}")

    with tempfile.TemporaryDirectory(prefix="kb-released-fixture-") as temporary:
        scratch = Path(temporary)
        binary = scratch / "kb-linux-amd64"
        subprocess.run([
            "gh", "release", "download", args.version, "--repo", REPOSITORY,
            "--pattern", binary.name, "--dir", str(scratch),
        ], check=True)
        digest = hashlib.sha256(binary.read_bytes()).hexdigest()
        if digest != args.sha256:
            raise SystemExit(f"release binary checksum {digest} does not match {args.sha256}")
        binary.chmod(0o700)
        data = scratch / "data"
        # No inherited provider keys, server URLs, user boards, or credentials.
        environment = {
            "HOME": str(scratch), "PATH": "/usr/bin:/bin", "KB_DATA": str(data),
            "KB_SECRET": SECRET,
        }
        old_format = args.version == "v0.2.0"
        for title, status, priority in [
            ("Migration high", "todo", 1),
            ("Migration medium", "doing", 2),
            ("Migration low", "done", 4 if old_format else 3),
        ]:
            command = [
                str(binary), "add", title, "--desc", "Synthetic migration fixture: " + title,
                "--status", status, "--prio", str(priority), "--tag", "fixture",
                "--due", "2030-01-02", "--effort", "M",
            ]
            if status != "done":
                command += ["--check", "Inspect migrated task"]
            if not old_format:
                command += ["--project", "migration"]
            subprocess.run(command, env=environment, check=True, text=True)

        database = data / "kb.db"
        # Every writer has exited. Do not freeze an incomplete WAL database.
        wal = data / "kb.db-wal"
        if wal.exists() and wal.stat().st_size:
            raise SystemExit("release left a nonempty WAL; stop and inspect before freezing")
        with sqlite3.connect(database.as_uri() + "?mode=ro", uri=True) as connection:
            connection.row_factory = sqlite3.Row
            schema = int(connection.execute(
                "SELECT v FROM meta WHERE k = 'schema_version'"
            ).fetchone()[0])
            tables = {row[0] for row in connection.execute(
                "SELECT name FROM sqlite_master WHERE type = 'table'"
            )}
            counts = {name: connection.execute(f'SELECT count(*) FROM "{name}"').fetchone()[0]
                      for name in ("tasks", "labels", "settings", "forge_sources", "import_links",
                                   "tombstones", "comments", "task_links") if name in tables}
            tasks = []
            for row in connection.execute("SELECT * FROM tasks ORDER BY title"):
                snapshot = {key: row[key] for key in (
                    "id", "title", "desc", "status", "prio", "due", "effort",
                    "created_at", "moved_at", "updated_at", "seq"
                ) if key in row.keys()}
                snapshot["tags"] = json.loads(row["tags"])
                snapshot["checks"] = json.loads(row["checks"])
                tasks.append(snapshot)
        frozen = database.read_bytes()
        manifest = {
            "version": args.version,
            "binary": {
                "asset": binary.name, "sha256": digest,
                "url": f"https://github.com/{REPOSITORY}/releases/download/{args.version}/{binary.name}",
            },
            "database_sha256": hashlib.sha256(frozen).hexdigest(),
            "schema_version": schema, "row_counts": counts, "tasks": tasks,
        }
        destination.mkdir(parents=True)
        shutil.copyfile(database, destination / "kb.db")
        (destination / "manifest.json").write_text(json.dumps(manifest, indent=2) + "\n")
        print(f"Frozen {args.version}: schema {schema}, {len(tasks)} tasks, {len(frozen)} bytes")


if __name__ == "__main__":
    main()
