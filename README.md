# nanit-controller

[![CI](https://github.com/keatsfonam/nanit-controller/actions/workflows/ci.yml/badge.svg)](https://github.com/keatsfonam/nanit-controller/actions/workflows/ci.yml)

Minimal controller that keeps selected [Nanit](https://www.nanit.com/) cameras publishing local RTMP to [MediaMTX](https://github.com/bluenviron/mediamtx), so [Frigate](https://frigate.video/) or any other NVR can use the stream without going through Nanit's cloud.

It does not serve RTMP, MQTT, Home Assistant entities, or sensor data. It only:

1. refreshes/persists Nanit auth state;
2. discovers configured babies/cameras;
3. opens Nanit WebSocket control connections;
4. sends `PUT_STREAMING STARTED` with an explicit RTMP URL;
5. monitors MediaMTX path readiness and re-requests streaming when the publisher disappears;
6. backs off per camera, releases WebSockets on Nanit connection-limit errors, and can reset stale local-streaming state with `STOPPED`/`STARTED`.

## Setup

You need a MediaMTX instance with its API enabled and its RTMP port reachable from the camera's network, plus a Nanit refresh token and the UIDs of the babies you want to stream.

Nanit logins require 2FA, so getting the first refresh token is interactive. The `init-nanit.sh` script from [indiefan/home_assistant_nanit](https://github.com/indiefan/home_assistant_nanit) handles this and writes a `session.json` that this controller reads as-is:

```sh
docker run --rm -it -v /path/to/data:/data indiefan/nanit ./init-nanit.sh
```

If you already have a refresh token from elsewhere, set `NANIT_BOOTSTRAP_REFRESH_TOKEN` instead. Refresh tokens are single-use: the controller rotates and persists them, so keep `/data` on durable storage.

Then:

```sh
docker run -d --name nanit-controller \
  -v /path/to/data:/data \
  -e NANIT_BABY_UIDS=0a1b2c3d \
  -e NANIT_RTMP_PUBLIC_ADDR=192.168.1.10:1935 \
  -e NANIT_MEDIAMTX_API_URL=http://192.168.1.10:9997 \
  -p 8080:8080 \
  ghcr.io/keatsfonam/nanit-controller:latest
```

On Kubernetes: single-replica Deployment, `/data` on a PersistentVolume, bootstrap token in a Secret. Don't run more than one replica; each instance takes up a Nanit "mobile app" connection slot.

## Configuration

Environment variables. Malformed durations or integers abort startup rather than silently falling back to defaults.

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

CI runs vet, build, tests (with `-race`), and a Docker build. Tags matching `v*` publish a multi-arch (amd64/arm64) image to `ghcr.io/keatsfonam/nanit-controller`.

## Credits

- [adam.stanek/nanit](https://gitlab.com/adam.stanek/nanit) reverse-engineered Nanit's local-streaming and WebSocket control protocol (WTFPL). None of this would work without that project.
- [indiefan/home_assistant_nanit](https://github.com/indiefan/home_assistant_nanit) is the maintained fork that added 2FA support. This controller imports its generated protobuf definitions (`pkg/client`) and shares its `session.json` format.

Not affiliated with Nanit. Everything here relies on undocumented APIs that can change without notice.

## License

[MIT](LICENSE)
