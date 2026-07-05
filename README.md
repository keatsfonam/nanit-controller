# nanit-controller

[![CI](https://github.com/keatsfonam/nanit-controller/actions/workflows/ci.yml/badge.svg)](https://github.com/keatsfonam/nanit-controller/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

A minimal controller that keeps selected [Nanit](https://www.nanit.com/) baby-monitor cameras publishing their local RTMP stream into [MediaMTX](https://github.com/bluenviron/mediamtx), so the feed can be consumed by [Frigate](https://frigate.video/) or any other NVR — no cloud round-trip for video.

```
Nanit camera ──RTMP──▶ MediaMTX ◀──RTSP/etc── Frigate / NVR
      ▲                    ▲
      │ websocket          │ path readiness (API)
      └──── nanit-controller ────┘
```

Unlike full-featured integrations, this project deliberately does **not** serve RTMP itself, speak MQTT, or expose Home Assistant entities or sensor data. It only:

1. refreshes and persists Nanit auth state (`session.json`);
2. discovers configured babies/cameras;
3. opens Nanit WebSocket control connections;
4. sends `PUT_STREAMING STARTED` with an explicit RTMP URL pointing at your MediaMTX instance;
5. monitors MediaMTX path readiness and re-requests streaming when the publisher disappears;
6. backs off per camera, releases WebSockets on Nanit connection-limit errors, and can reset stale local-streaming state with `STOPPED`/`STARTED`;
7. enforces WebSocket read/write deadlines with ping-based liveness, and reconnects after 3 consecutive failed streaming requests instead of looping on a dead connection;
8. serializes token refreshes across cameras (Nanit rotates refresh tokens; concurrent refreshes can invalidate the session) and only force-refreshes when a WebSocket handshake is rejected with 401/403;
9. falls back to the baby list cached in the session file (or retries with backoff) when discovery fails at startup, instead of crash-looping.

## Getting started

### Prerequisites

- A running MediaMTX instance whose RTMP port is reachable **from the camera's network**, with its API enabled.
- A Nanit account refresh token (see below).
- Your baby UID(s) — visible in the Nanit app/web URLs, or in `session.json` after first authentication.

### Acquire a refresh token

Nanit requires 2FA, so the initial login is interactive. The easiest way is the `init-nanit.sh` helper from the [indiefan/home_assistant_nanit](https://github.com/indiefan/home_assistant_nanit) project, which writes a `session.json` this controller can read directly:

```sh
docker run --rm -it -v /path/to/data:/data indiefan/nanit ./init-nanit.sh
```

Alternatively, set `NANIT_BOOTSTRAP_REFRESH_TOKEN` to a refresh token obtained any other way; the controller bootstraps its own `session.json` from it. Note that Nanit refresh tokens are single-use: the controller rotates and persists them, so the session file must live on durable storage.

### Run

```sh
docker run -d --name nanit-controller \
  -v /path/to/data:/data \
  -e NANIT_BABY_UIDS=0a1b2c3d \
  -e NANIT_RTMP_PUBLIC_ADDR=192.168.1.10:1935 \
  -e NANIT_MEDIAMTX_API_URL=http://192.168.1.10:9997 \
  -p 8080:8080 \
  ghcr.io/keatsfonam/nanit-controller:latest
```

On Kubernetes, run it as a single-replica Deployment with `/data` on a PersistentVolume and the bootstrap token in a Secret. Keep one replica: each controller consumes Nanit "mobile app" connection slots.

## Configuration

Environment variables. Malformed duration or integer values abort startup with an error naming the offending variable (they are not silently replaced with defaults).

| Name | Default | Description |
| --- | --- | --- |
| `NANIT_SESSION_FILE` | `/data/session.json` | Persistent mutable session path. |
| `NANIT_BOOTSTRAP_REFRESH_TOKEN` | empty | Secret bootstrap refresh token, used when the session has no refresh token. |
| `NANIT_BABY_UIDS` | required | Comma-separated baby UID allowlist, e.g. `0a1b2c3d,4e5f6a7b`. |
| `NANIT_RTMP_PUBLIC_ADDR` | required | Camera-reachable RTMP host:port, e.g. `192.168.130.129:1935`. |
| `NANIT_RTMP_PATH_PREFIX` | `/local` | RTMP path prefix. |
| `NANIT_MEDIAMTX_API_URL` | `http://127.0.0.1:9997` | MediaMTX API base URL. |
| `NANIT_CHECK_INTERVAL` | `20s` | MediaMTX path check interval. |
| `NANIT_MISSING_GRACE` | `30s` | Missing-path grace period before re-requesting. |
| `NANIT_REREQUEST_INTERVAL` | `60s` | Minimum interval between PUT_STREAMING requests per camera. |
| `NANIT_MISSING_PUBLISHER_RESTART_RETRIES` | `3` | Missing-publisher re-requests before sending `PUT_STREAMING STOPPED` then `STARTED`. Set `0` to disable reset. |
| `NANIT_RETRY_BACKOFF_INITIAL` | `15s` | Initial per-camera reconnect backoff for ordinary failures. |
| `NANIT_RETRY_BACKOFF_MAX` | `15m` | Maximum per-camera exponential backoff. |
| `NANIT_CONNECTION_LIMIT_BACKOFF` | `5m` | Initial per-camera backoff when Nanit reports mobile app connection limit. |
| `NANIT_REQUEST_TIMEOUT` | `30s` | WebSocket request timeout. |
| `NANIT_HEALTH_ADDR` | `:8080` | Health endpoint address. |
| `NANIT_LOG_LEVEL` | `info` | `debug` or `info`. |

## Health and status

`/healthz` is a lightweight liveness endpoint that returns `ok`.

`/readyz` returns HTTP 200 with a JSON snapshot for each configured camera. Camera-level degraded states such as `connection_limited` or `publisher_missing` are intentionally reported in the JSON body without failing the HTTP probe, because Kubernetes restarts can worsen Nanit mobile connection-slot exhaustion.

Example fields include `publisher_present`, `missing_since`, `missing_retry_count`, `last_request_status`, `last_success_at`, `last_error`, `consecutive_connection_limit_failures`, and `backoff_until`.

## Development

```sh
go vet ./...
go test -race ./...
go run ./cmd/nanit-controller
```

CI runs vet, build, race-enabled tests, and a Docker build on every push and pull request. Pushing a `v*` tag publishes a multi-arch (amd64/arm64) image to `ghcr.io/keatsfonam/nanit-controller`.

## Attribution

This controller is a small, from-scratch implementation, but it stands on the shoulders of the people who reverse-engineered the Nanit protocol:

- [adam.stanek/nanit](https://gitlab.com/adam.stanek/nanit) — the original Nanit local-streaming project (WTFPL), which figured out the RTMP local-streaming and WebSocket control protocol this whole ecosystem relies on.
- [indiefan/home_assistant_nanit](https://github.com/indiefan/home_assistant_nanit) — the maintained fork that added support for Nanit's now-required 2FA authentication. This project imports its generated protobuf definitions (`pkg/client`) for the WebSocket protocol, and its `init-nanit.sh` is the recommended way to obtain a refresh token. The `session.json` format is compatible.

## Disclaimer

This is an unofficial, community project. It is not affiliated with, endorsed by, or supported by Nanit. It uses undocumented APIs that may change or break at any time. Use at your own risk.

## License

[MIT](LICENSE)
