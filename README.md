# Flowmon

A self-hosted dashboard for network traffic. Flowmon collects IPFIX and NetFlow from your router (MikroTik first, but any exporter that follows the standard), and shows where traffic goes and who is on the other end: country, ASN owner, reverse DNS and registration details.

> **Status: early development.** This is the project skeleton: one binary that serves the web UI. Collection, storage and the dashboards are tracked in [issue #1](https://github.com/jkop-xyz/flowmon/issues/1).

## Run it

```sh
docker build -t flowmon .
docker run -d --name flowmon --network host -v flowmon-data:/data flowmon
```

Then open <http://localhost:8080>. Host networking is how Flowmon will run once collection exists, because it needs to see each exporter's real source address. Release images, a compose file and a Podman Quadlet come in [#19](https://github.com/jkop-xyz/flowmon/issues/19).

## Configuration

| Variable | Default | Meaning |
|---|---|---|
| `FLOWMON_HTTP_ADDR` | `:8080` | Address for the web UI and API |
| `FLOWMON_DATA_DIR` | `/data` | Where Flowmon keeps its data |
| `FLOWMON_LOG_LEVEL` | `info` | `debug`, `info`, `warn` or `error` |
| `FLOWMON_LOG_FORMAT` | `text` | `text` or `json` |

`flowmon healthcheck` exits 0 when the local server is healthy. Use it in run configs: Podman ignores the image's `HEALTHCHECK` for OCI images.

## Develop

You need Go 1.27 and Node 26.

```sh
make build        # build the UI, then bin/flowmon with the UI embedded
make test         # Go vet and tests, frontend lint
make dev          # run the server on :8080
cd web && npm run dev   # UI with hot reload on :5173, proxying /api to :8080
```

The design lives in [`GLOSSARY.md`](GLOSSARY.md) (the domain language) and [`docs/adr/`](docs/adr) (the decisions and why they were made).

## Licence

[Apache-2.0](LICENSE). Flowmon will use GeoIP data from MaxMind GeoLite2 (downloaded with your own licence key) or DB-IP Lite (CC BY 4.0, attribution shown in the UI). Neither is bundled in the image.
