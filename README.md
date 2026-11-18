# Flowmon

A self-hosted dashboard for network traffic. Flowmon collects IPFIX and NetFlow from your router (MikroTik first, but any exporter that follows the standard), and shows where traffic goes and who is on the other end: country, ASN owner, reverse DNS and registration details.

> **Status: early development.** Flowmon receives IPFIX over UDP and shows each Exporter and its Templates on the Exporters page. Storage and the dashboards are next; progress is tracked in [issue #1](https://github.com/jkop-xyz/flowmon/issues/1).

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
| `FLOWMON_FLOW_UDP_ADDRS` | `:2055,:4739` | Comma-separated UDP addresses to receive flow export on. Both ports accept any version; the datagram says which it is |
| `FLOWMON_MAX_EXPORTERS` | `64` | Most Exporters tracked; datagrams from further sources are ignored and counted |
| `FLOWMON_DEBUG_CAPTURE` | off | Path of a pcap file to record received datagrams to (see below) |
| `FLOWMON_DEBUG_CAPTURE_MAX_MB` | `64` | Size at which the capture file is rotated |
| `FLOWMON_DEBUG_CAPTURE_FILES` | `3` | Capture files kept, including the current one |
| `FLOWMON_DEBUG_CAPTURE_MAX_DURATION` | `1h` | Capturing stops this long after start |

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
