# nanit-controller

[![CI](https://github.com/keatsfonam/nanit-controller/actions/workflows/ci.yml/badge.svg)](https://github.com/keatsfonam/nanit-controller/actions/workflows/ci.yml)

`nanit-controller` asks selected Nanit cameras to publish their video directly
to a local [MediaMTX](https://github.com/bluenviron/mediamtx) instance. The
video payload stays on the configured network path; authentication and camera
control still use Nanit's HTTPS and WebSocket services.

The controller does not serve RTMP/RTSP, publish MQTT data, create Home
Assistant entities, or process sensor data. It:

1. refreshes and durably persists Nanit authentication state;
2. discovers the configured baby and camera records;
3. opens one Nanit control WebSocket per selected camera;
4. requests local RTMP publishing;
5. checks MediaMTX for each publisher and retries or resets stale streams; and
6. exposes liveness, startup-readiness, and redacted camera status endpoints.

> [!WARNING]
> This project uses undocumented Nanit APIs and an independently
> reverse-engineered protocol. Nanit can change either without notice. It is
> not affiliated with or endorsed by Nanit. Review Nanit's terms and your local
> privacy requirements before use.

## Prerequisites

- a Nanit refresh token;
- the baby UIDs to allow;
- one durable, writable session directory;
- MediaMTX with its control API reachable by the controller;
- an RTMP listener reachable from the camera network; and
- `curl` plus `jq` for the bootstrap examples.

Use exactly one controller instance for a session file and camera set. Refresh
tokens rotate after use, and each control WebSocket consumes a Nanit mobile-app
connection slot.

## Bootstrap authentication

Nanit login requires an interactive 2FA flow. The external
[`indiefan/home_assistant_nanit`](https://github.com/indiefan/home_assistant_nanit)
image provides an initializer. Pin the version and override its entrypoint:

```sh
DATA_DIR=/srv/nanit-controller
sudo install -d -m 0700 "$DATA_DIR"

docker run --rm -it \
  --mount "type=bind,src=$DATA_DIR,dst=/data" \
  --entrypoint=/app/scripts/init-nanit.sh \
  indiefan/nanit:1.3.5@sha256:b59d945afbbeae0482b8d6af54176775c4a1ab598c687d86d136806f433f4b7a

sudo chown -R 65532:65532 "$DATA_DIR"
sudo chmod 0700 "$DATA_DIR"
sudo chmod 0600 "$DATA_DIR/session.json"
```

The ownership step is required because the controller image runs as UID/GID
65532 and must replace `session.json` during token rotation.

To list baby UIDs from the newly initialized session, use the same undocumented
endpoint used by the controller. Keep the token out of logs and shell history:

```sh
auth_token="$(sudo jq -r .authToken "$DATA_DIR/session.json")"
curl --fail --silent --show-error \
  -H "Authorization: $auth_token" \
  https://api.nanit.com/babies |
  jq -r '.babies[] | [.uid, .name] | @tsv'
unset auth_token
```

If a trusted process supplies a fresh token another way, set
`NANIT_BOOTSTRAP_REFRESH_TOKEN`. The session file takes precedence once it
contains a refresh token.

## Run the controller

The following assumes MediaMTX is already configured. The controller's
MediaMTX API request must be authorized; current MediaMTX defaults permit API
access only from loopback. Prefer the Kubernetes sidecar example below. This
client does not send MediaMTX API credentials. For a remote API, add a
MediaMTX `user: any` API permission restricted to the controller's source IP
and firewall port 9997 to that same source; never expose it broadly.

The behavior documented here is newer than the published `v0.1.0` image. Build
this checkout before using the following command:

```sh
docker build -t nanit-controller:local .

docker run -d --name nanit-controller \
  --restart unless-stopped \
  --read-only \
  --cap-drop ALL \
  --security-opt no-new-privileges \
  --mount "type=bind,src=$DATA_DIR,dst=/data" \
  -e NANIT_BABY_UIDS=0a1b2c3d \
  -e NANIT_RTMP_PUBLIC_ADDR=192.168.1.10:1935 \
  -e NANIT_MEDIAMTX_API_URL=http://192.168.1.10:9997 \
  -p 127.0.0.1:8080:8080 \
  nanit-controller:local
```

For a registry deployment, publish a stable release from this tree and pin its
multi-architecture digest. Do not use `v0.1.0` with the health, privacy, or
recovery behavior documented below.

For Kubernetes, see [`examples/kubernetes`](examples/kubernetes). It keeps the
MediaMTX API on pod loopback, requires RTSP reader credentials, restricts
publisher paths and source networks, and disables service-account credentials.

## Configuration

Malformed or unsafe values abort startup.

| Name | Default | Description |
| --- | --- | --- |
| `NANIT_SESSION_FILE` | `/data/session.json` | Mutable session path on durable storage. |
| `NANIT_BOOTSTRAP_REFRESH_TOKEN` | empty | Initial secret used only when the session has no refresh token. |
| `NANIT_BABY_UIDS` | required | Unique comma-separated baby UID allowlist. |
| `NANIT_RTMP_PUBLIC_ADDR` | required | Camera-reachable RTMP `host:port`. |
| `NANIT_RTMP_PATH_PREFIX` | `/local` | RTMP path prefix containing safe path segments. |
| `NANIT_MEDIAMTX_API_URL` | `http://127.0.0.1:9997` | MediaMTX API origin; credentials and paths are rejected. |
| `NANIT_CHECK_INTERVAL` | `20s` | MediaMTX path-check interval. |
| `NANIT_MISSING_GRACE` | `30s` | Time from first observed absence before re-requesting. |
| `NANIT_REREQUEST_INTERVAL` | `60s` | Minimum interval between `PUT_STREAMING STARTED` requests. |
| `NANIT_MISSING_PUBLISHER_RESTART_RETRIES` | `3` | Re-requests allowed before a later check sends `STOPPED` then `STARTED`; `0` disables reset. |
| `NANIT_RETRY_BACKOFF_INITIAL` | `15s` | Initial reconnect backoff. |
| `NANIT_RETRY_BACKOFF_MAX` | `15m` | Hard maximum reconnect backoff, including jitter. |
| `NANIT_CONNECTION_LIMIT_BACKOFF` | `5m` | Initial backoff after a Nanit connection-limit response. |
| `NANIT_REQUEST_TIMEOUT` | `30s` | WebSocket request timeout. |
| `NANIT_HEALTH_ADDR` | `:8080` | Health listener address. |
| `NANIT_LOG_LEVEL` | `info` | `debug`/`trace`, `info`, `warn`/`warning`, or `error`. |

## Health and status

- `/healthz` returns HTTP 200 while the process and health server are running.
- `/readyz` returns HTTP 503 only while initial discovery is incomplete. After
  discovery it returns HTTP 200 even when a camera is degraded. Kubernetes
  readiness failures remove endpoints; they do not restart containers.
- `/statusz` always returns HTTP 200 with the same JSON snapshot for monitoring.

The status payload contains baby UIDs, local path names, state, retry timing,
and recent errors. Baby names, camera UIDs, RTMP destinations, and tokens are
not exposed. Keep the listener private nonetheless.

## Session durability and recovery

A refresh response invalidates the previous refresh token. Session updates are
therefore written to a mode-0600 same-directory recovery file, synced, renamed,
and followed by a directory sync before the new token is used. The storage
backend must provide reliable same-filesystem rename and `fsync` semantics.

If the refresh request outcome is uncertain, or persistence fails after Nanit
rotates a token, the client enters a sticky error state instead of retrying a
possibly consumed token. A complete mode-0600 recovery candidate is retained
and logged when one exists. Do not let another instance retry the old token.

1. Stop or scale the controller to zero.
2. If the error says the session was committed, preserve the current
   `session.json`; it contains the rotated token even though the directory sync
   failed. Copy it safely before rebooting the storage host.
3. Otherwise, if the logged `.nanit-session-recovery-*` file is complete,
   move it to `session.json` on the same volume and restore owner
   `65532:65532`, directory mode `0700`, and file mode `0600`.
4. If neither candidate exists, obtain a new refresh token. Remove the stale
   session only while the controller is stopped, then install the new session
   or update the bootstrap secret.
5. Start one controller and inspect `/statusz`.

Restoring an old PVC snapshot commonly restores an already-consumed token; it
is not a token backup strategy. Protect the live PVC and any backups as account
credentials.

If Nanit reports connection-slot exhaustion, first confirm that no duplicate
controller, old upstream relay, or Nanit app session is still connected. The
controller releases its WebSocket before applying the longer connection-limit
backoff.

## Known operational limits

- Camera discovery occurs at startup. Account or camera changes require a
  controller restart.
- RTMP and RTSP are plaintext unless separately protected. Restrict them to
  trusted networks or add MediaMTX-supported encryption.
- Publisher authentication is not enabled in the example because Nanit camera
  support for MediaMTX's RTMP credential query parameters has not been verified.
- No metrics endpoint is provided; monitor `/statusz` and logs.

## Development

Go 1.26 and `protoc` are required. The committed protobuf binding was generated
with `protoc` 27.0 and `protoc-gen-go` v1.34.2.

```sh
make generate   # regenerate internal/protocol/nanitpb/nanit.pb.go
make check      # format, module, vet, race-test, and build checks
make vuln       # govulncheck ./...
```

After exporting the required runtime configuration, run locally with:

```sh
NANIT_SESSION_FILE="$PWD/session.json" go run ./cmd/nanit-controller
```

CI also renders the Kubernetes example and builds the container. Stable
semantic version tags such as `v0.2.0` run the same validation before publishing
amd64/arm64 images with BuildKit SBOM and provenance attestations. GitHub
Actions are pinned to reviewed commit SHAs.

## Security reports

Follow [`SECURITY.md`](SECURITY.md). Never attach tokens, session files,
credentials, identifiers, or recordings to a public issue.

## Credits and licensing

- [Adam Staněk's nanit project](https://gitlab.com/adam.stanek/nanit)
  documented the reverse-engineered local-streaming and WebSocket protocol.
- [`indiefan/home_assistant_nanit`](https://github.com/indiefan/home_assistant_nanit)
  provided the 2FA-era compatibility reference and token initializer.

The controller is licensed under [MIT](LICENSE). Binary and image distributions
include [`THIRD_PARTY_NOTICES.md`](THIRD_PARTY_NOTICES.md). The local reduced
protocol schema replaces the previously linked fork dependency; see the notice
file for provenance and license details.
