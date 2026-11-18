# Flowmon

A self-hosted dashboard for network traffic. Flowmon collects IPFIX and NetFlow from your router (MikroTik first, but any exporter that follows the standard), and shows where traffic goes and who is on the other end: country, ASN owner, reverse DNS and registration details.

> **Status: early development.** Flowmon receives IPFIX over UDP, stores Flows and minute/hour Rollups in an embedded DuckDB under `/data`, and shows bandwidth and top Local Hosts, protocols and Service Ports on the Overview, and each Exporter with its Templates on the Exporters page. GeoIP enrichment and the other screens are next; progress is tracked in [issue #1](https://github.com/jkop-xyz/flowmon/issues/1).

## Run it

```sh
docker build -t flowmon .
docker run -d --name flowmon --network host -v flowmon-data:/data flowmon
```

Then open <http://localhost:8080>. The first visit runs setup: enter the one-time setup token from the logs (`docker logs flowmon`), choose where accounts and settings live (built-in SQLite, or your own PostgreSQL), and create the admin account. Forgot the password? `docker exec -it flowmon flowmon admin reset-password admin`. Host networking is how Flowmon will run once collection exists, because it needs to see each exporter's real source address. Release images, a compose file and a Podman Quadlet come in [#19](https://github.com/jkop-xyz/flowmon/issues/19).

Setting up a MikroTik? See [docs/mikrotik-setup.md](docs/mikrotik-setup.md) (also in the app, linked from the Exporters page).

## Configuration

| Variable | Default | Meaning |
|---|---|---|
| `FLOWMON_HTTP_ADDR` | `:8080` | Address for the web UI and API |
| `FLOWMON_DATA_DIR` | `/data` | Where Flowmon keeps its data |
| `FLOWMON_LOG_LEVEL` | `info` | `debug`, `info`, `warn` or `error` |
| `FLOWMON_LOG_FORMAT` | `text` | `text` or `json` |
| `FLOWMON_FLOW_UDP_ADDRS` | `:2055,:4739` | Comma-separated UDP addresses to receive flow export on. Both ports accept any version; the datagram says which it is |
| `FLOWMON_LOCAL_NETWORKS` | none | Extra Local Networks (comma-separated CIDRs) on top of the defaults: RFC 1918, ULA and link-local. 100.64.0.0/10 isn't a default; add it if you run Tailscale or similar |
| `FLOWMON_FLOW_STORE_MEMORY_LIMIT` | `1GB` | DuckDB memory limit for the Flow Store |
| `FLOWMON_FLOW_STORE_THREADS` | DuckDB's default | DuckDB threads for the Flow Store |
| `FLOWMON_RETENTION_RAW` | `7d` | How long raw Flows are kept (whole days are dropped) |
| `FLOWMON_RETENTION_MINUTE` | `90d` | How long minute Rollups are kept |
| `FLOWMON_RETENTION_HOUR` | `730d` | How long hour Rollups are kept |
| `FLOWMON_WATERMARK` | learned | Override the per-Exporter Watermark (how long after a minute its data may still change), e.g. `5m` |
| `FLOWMON_DATABASE_URL` | setup screen | App Store location, e.g. `sqlite:///data/flowmon.db` or `postgres://flowmon@db.lan:5432/lab?sslmode=require&search_path=flowmon`. When set, it overrides and locks the setup screen's choice |
| `FLOWMON_DATABASE_PASSWORD_FILE` | none | File holding the Postgres password (e.g. a Docker secret) |
| `FLOWMON_ADMIN_USERNAME`, `FLOWMON_ADMIN_PASSWORD` (or `_FILE`) | none | Create the admin account on first start instead of in the setup screen |
| `FLOWMON_TRUSTED_PROXIES` | none | CIDRs of reverse proxies whose `X-Forwarded-For`/`-Proto` headers are trusted |
| `FLOWMON_SECURE_COOKIES` | `auto` | `auto` marks the session cookie Secure when the browser used HTTPS (directly or via a trusted proxy); `always` or `never` force it |
| `FLOWMON_EXPORTER_AUTO_ACCEPT` | `false` | Approve every new Exporter without asking. Otherwise a new Exporter waits on the Exporters page, its Templates kept and its Flows dropped |
| `FLOWMON_EXPORTER_ALLOWLIST` | none | CIDRs whose Exporters are approved without asking, e.g. `192.168.10.1/32` |
| `FLOWMON_MAX_EXPORTERS` | `64` | Most Exporters tracked; datagrams from further sources are ignored and counted |
| `FLOWMON_DEBUG_CAPTURE` | off | Path of a pcap file to record received datagrams to (see below) |
| `FLOWMON_DEBUG_CAPTURE_MAX_MB` | `64` | Size at which the capture file is rotated |
| `FLOWMON_DEBUG_CAPTURE_FILES` | `3` | Capture files kept, including the current one |
| `FLOWMON_DEBUG_CAPTURE_MAX_DURATION` | `1h` | Capturing stops this long after start |

### SNMP and Reconciliation

On the Exporters page, each approved Exporter can be polled over SNMP (v3 recommended, or v2c) every minute. SNMP fills in interface names (`ifName`), and for every WAN Interface Flowmon compares the router's own 64-bit interface counters (minus layer-2 overhead per packet) with the bytes in Flows, per hour. A gap above 15% for 3 hours in a row is flagged: usually hardware offload that Traffic Flow can't see, a long active timeout, or the wrong overhead preset. SNMP credentials are stored encrypted with a key kept in `/data/secret.key`; back that file up with the rest of `/data`.

### Debug capture

`FLOWMON_DEBUG_CAPTURE=/data/debug/capture.pcap` records every datagram Flowmon receives, wrapped in IP and UDP headers so Wireshark and tshark decode it as IPFIX. Use it when an Exporter shows up as "0% decoded" or its numbers look wrong. Captures contain who talked to whom on your network: the files are created readable only by Flowmon's user, the capture stops on its own, and you should anonymise a capture before sharing it.

`flowmon healthcheck` exits 0 when the local server is healthy. Use it in run configs: Podman ignores the image's `HEALTHCHECK` for OCI images.

## Develop

You need Go 1.27 and Node 26.

```sh
make build        # build the UI, then bin/flowmon with the UI embedded
make test         # Go vet and tests, frontend lint
make dev          # run the server on :8080
cd web && npm run dev   # UI with hot reload on :5173, proxying /api to :8080
go run ./internal/devtools/sendipfix -addr 127.0.0.1:4739   # synthetic MikroTik-like IPFIX
```

The design lives in [`GLOSSARY.md`](GLOSSARY.md) (the domain language) and [`docs/adr/`](docs/adr) (the decisions and why they were made).

## Licence

[Apache-2.0](LICENSE). Flowmon will use GeoIP data from MaxMind GeoLite2 (downloaded with your own licence key) or DB-IP Lite (CC BY 4.0, attribution shown in the UI). Neither is bundled in the image.
