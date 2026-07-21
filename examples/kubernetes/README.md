# Kubernetes example

This example runs `nanit-controller` and MediaMTX in one pod. The controller
uses the MediaMTX API over `127.0.0.1`; only RTMP and RTSP are published by the
Service.

It is a hardened starting point, not a drop-in production manifest. Before
applying it, you must set camera/NVR networks, reserve a LoadBalancer address, choose
credentials, replace controller placeholders, and add resource policy based on
observed load.

## Security model

- The namespace enforces the Kubernetes restricted Pod Security Standard.
- Both containers run as UID/GID 65532 with read-only root filesystems, no
  Linux capabilities, no privilege escalation, and no service-account token.
- The MediaMTX API listens only on pod loopback.
- RTSP readers require credentials from `nanit-secret`.
- Anonymous RTMP publishing is limited to the configured camera CIDR and
  `local/<baby-uid>` paths. Publisher credentials are not enabled because
  Nanit camera support for MediaMTX's RTMP credential query parameters is
  unverified.
- The LoadBalancer accepts only the configured source ranges and preserves
  source IPs for MediaMTX's publisher CIDR check.

RTMP and RTSP remain plaintext. Use trusted networks, firewall policy, or a
MediaMTX-supported encrypted transport. Do not expose these ports to the
internet.

## Configure

All commands below run from the repository root.

1. In `configmap.yaml`, replace both `192.168.1.0/24` entries with the camera
   and NVR networks. Keep the API address on `127.0.0.1`.
2. In `service.yaml`, replace `loadBalancerSourceRanges` with the union of
   those trusted networks and configure a reserved external IP using your
   load-balancer implementation.
3. In `deployment.yaml`, replace:
   - `REPLACE_WITH_BABY_UID` with the comma-separated baby UID allowlist;
   - `REPLACE_WITH_LOAD_BALANCER_IP:1935` with the camera-reachable RTMP
     address.
   If you change `NANIT_RTMP_PATH_PREFIX`, update both MediaMTX permission
   regular expressions to match it.
4. Build and publish a stable controller release from this tree. Replace
   `REPLACE_WITH_RELEASE` with its patch tag and multi-architecture digest,
   such as `<version>@sha256:<digest>`. Published `v0.1.0` predates the health,
   privacy, and recovery behavior documented here.
5. MediaMTX is pinned to a verified patch tag and digest. Update both only
   after testing the new digest in your environment.

The source-range and MediaMTX CIDR rules must agree. `externalTrafficPolicy:
Local` preserves the client address; confirm that your load balancer routes to
the node hosting the pod.

## Create secrets

Create the namespace first, then create one Secret containing the fresh Nanit
refresh token and RTSP reader credentials:

```sh
kubectl apply -f examples/kubernetes/namespace.yaml

kubectl -n nanit create secret generic nanit-secret \
  --from-literal=refresh-token='REPLACE_WITH_FRESH_TOKEN' \
  --from-literal=rtsp-username='nvr' \
  --from-literal=rtsp-password='REPLACE_WITH_RANDOM_PASSWORD'
```

Avoid leaving real values in shell history. A sealed/encrypted Secret workflow
is preferable for GitOps. Restrict Secret and PVC RBAC: either grants access to
a credential that can control the Nanit account.

The refresh token is single-use. Do not start an old relay or second controller
with the same token/session.

## Deploy

```sh
kubectl apply -k examples/kubernetes
kubectl -n nanit rollout status deployment/nanit-relay
```

The controller readiness probe returns 503 only until initial discovery
completes. Camera degradation remains HTTP 200 and is represented in the JSON
body so readiness does not remove the MediaMTX pod from the Service.

Inspect redacted status without exposing the health port through a Service:

```sh
kubectl -n nanit port-forward deployment/nanit-relay 8080:8080
curl --fail http://127.0.0.1:8080/statusz
```

Read a stream with the RTSP credentials:

```text
rtsp://nvr:REPLACE_WITH_URL_ENCODED_PASSWORD@192.168.1.10:8554/local/0a1b2c3d
```

For Frigate:

```yaml
cameras:
  nursery:
    ffmpeg:
      inputs:
        - path: rtsp://nvr:REPLACE_WITH_URL_ENCODED_PASSWORD@192.168.1.10:8554/local/0a1b2c3d
          roles: [detect, record]
```

## Operations

- `strategy: Recreate` and one replica are required. The PVC is RWO, refresh
  tokens rotate, and every extra controller consumes a Nanit connection slot.
- The PVC uses the default StorageClass. Confirm that it supports durable
  same-filesystem rename plus file and directory `fsync`; snapshots of an old
  session usually contain an already-consumed token.
- The manifests intentionally omit generic CPU/memory values. Add requests,
  memory limits, and alerts from measurements for your camera count and stream
  profile rather than copying arbitrary limits.
- Add a CNI-specific NetworkPolicy if your cluster does not already restrict
  pod ingress and egress. Preserve DNS plus Nanit HTTPS/WSS egress and the
  trusted RTMP/RTSP source ranges.
- Back up configuration, not stale tokens. Follow the root README's session
  recovery procedure after PVC or token-persistence failure.
- Before changing reader credentials, update the Secret and restart the
  Deployment. Before replacing the refresh token or session, scale to zero.

Deleting the namespace deletes the Secret and PVC. Whether the underlying
volume data survives depends on the storage reclaim policy. Verify retention
before teardown.
