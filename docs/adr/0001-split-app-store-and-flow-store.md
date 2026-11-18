# Separate App Store (SQLite or Postgres) and Flow Store (DuckDB)

Flowmon keeps two kinds of data, each in its own store.

- The **App Store** holds users, identities, sessions, settings, labels and lookup caches. It is built-in SQLite or an external Postgres, chosen at first-run setup or through `FLOWMON_DATABASE_URL`.
- The **Flow Store** holds raw Flows and Rollups in embedded DuckDB. A home network produces roughly 60M raw flows a week. That is an analytical, columnar workload, where Postgres or SQLite would be several times bigger on disk and seconds slower on top-N queries. DuckDB still keeps the "one image, one `/data` volume" setup.

## Considered and rejected: SQLite-only App Store

Three independent design reviews recommended dropping Postgres for the App Store. Their reasons: the flows live in DuckDB under `/data` anyway, so Postgres doesn't simplify backups, and supporting two engines doubles migration testing. **We keep Postgres on purpose.** Users who already run Postgres keep their user, settings and label data in the database they already back up, monitor and manage. Postgres is also the realistic base for multi-user and OIDC later. Do not "simplify" it away.

## Consequences

- Flows are not stored in Postgres. Only app state is.
- App Store migrations must work on both SQLite and Postgres and are tested in CI against both.
- The Flow Store sits behind an interface of named query methods, one per screen or widget. A general query-description layer waits until a second flow backend (ClickHouse or Postgres) actually exists.
- Raw Flows are stored in one table per day, so retention and the disk cap free space by dropping whole days, because DuckDB `DELETE` does not shrink the file. The disk cap never deletes raw Flows that haven't been rolled up yet.
- DuckDB needs CGO and glibc. The image is Debian-slim based, and arm64 is built on native runners.
- Moving the App Store from one engine to the other after setup is out of scope for v1.
