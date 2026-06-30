# Nanit Controller

Minimal controller that keeps selected Nanit cameras publishing local RTMP to MediaMTX for Frigate.

It does not serve RTMP, MQTT, Home Assistant entities, or sensor data. It only:

1. refreshes/persists Nanit auth state;
2. discovers configured babies/cameras;
3. opens Nanit WebSocket control connections;
4. sends `PUT_STREAMING STARTED` with an explicit RTMP URL;
5. monitors MediaMTX path readiness and re-requests streaming when the publisher disappears.

## Configuration

Environment variables:

| Name | Default | Description |
| --- | --- | --- |
| `NANIT_SESSION_FILE` | `/data/session.json` | Persistent mutable session path. |
| `NANIT_BOOTSTRAP_REFRESH_TOKEN` | empty | Secret bootstrap refresh token, used when the session has no refresh token. |
| `NANIT_BABY_UIDS` | required | Comma-separated baby UID allowlist, e.g. `ef693503,b968ee9f`. |
| `NANIT_RTMP_PUBLIC_ADDR` | required | Camera-reachable RTMP host:port, e.g. `192.168.130.129:1935`. |
| `NANIT_RTMP_PATH_PREFIX` | `/local` | RTMP path prefix. |
| `NANIT_MEDIAMTX_API_URL` | `http://127.0.0.1:9997` | MediaMTX API base URL. |
| `NANIT_CHECK_INTERVAL` | `20s` | MediaMTX path check interval. |
| `NANIT_MISSING_GRACE` | `30s` | Missing-path grace period before re-requesting. |
| `NANIT_REREQUEST_INTERVAL` | `60s` | Minimum interval between PUT_STREAMING requests per camera. |
| `NANIT_CONNECTION_LIMIT_BACKOFF` | `5m` | Backoff when Nanit reports mobile app connection limit. |
| `NANIT_REQUEST_TIMEOUT` | `30s` | WebSocket request timeout. |
| `NANIT_HEALTH_ADDR` | `:8080` | Health endpoint address. |
| `NANIT_LOG_LEVEL` | `info` | `debug` or `info`. |

## Local test

```sh
go test ./...
go run ./cmd/nanit-controller
```
