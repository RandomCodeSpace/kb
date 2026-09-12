# Released database fixtures

These databases were created by published Linux amd64 binaries using only synthetic
records. They are frozen inputs, never regenerated from the current migrations.

| Release | Role for v1.11.0 | Original schema | Tasks |
| --- | --- | --- | --- |
| v0.2.0 | Oldest SQLite release supported by the upgrade check | 1 | 3 |
| v1.10.0 | Previous minor release | 11 | 3 |

Each directory contains `kb.db` and `manifest.json`. The manifest records the
release asset URL and SHA-256, database SHA-256, original schema, table row counts,
and historical task contents. The older fixture includes priority 4, which current
versions must migrate to priority 3. The known test secret is
`synthetic-migration-fixture-secret-only`; no real credentials or user data are used.

`TestReleasedDatabaseFixturesMigrate` verifies each checksum, copies every fixture
to a temporary directory, checks its historical schema, opens it with the current
store, and checks schema version, row counts, and task contents. Opening it again
must preserve the migrated tasks. The original files must remain byte-for-byte
unchanged. The `migration-recovery` job owns this check; its workflow selection
is maintained with the readiness CI changes.

## Add a fixture after every release

After publishing each release, obtain the `kb-linux-amd64` SHA-256 from the release
checksum file and independently compare it with GitHub's asset digest. For v0.2.0,
which has no checksum file, the recorded digest comes from GitHub's asset metadata.
On Linux amd64 with Python 3 and an authenticated `gh` CLI, run from the repository:

```sh
python3 scripts/generate-migration-fixtures.py VERSION BINARY_SHA256
```

The script downloads that exact release asset, verifies its checksum before
execution, and creates three synthetic tasks with the released CLI in an isolated
environment. It inspects the closed database without modifying it and refuses to
overwrite an existing version directory. Timestamps and task UUIDs come from the
released binary; regeneration is not expected to produce identical bytes.

Commit the new directory and run:

```sh
go test -p 2 ./internal/store -run '^TestReleasedDatabaseFixturesMigrate$' -count=1
```

Keep historical fixture directories unchanged. Add one for every published release,
including patch releases; the oldest SQLite release and previous minor must remain
covered. If a future released CLI changes the seed command, update the generator
for that release without rebuilding historical databases. Current code is never
used to generate a historical fixture.
