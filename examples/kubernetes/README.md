# Kubernetes example

nanit-controller runs as a sidecar to MediaMTX, talking to the MediaMTX API over localhost so the API never needs to be exposed. A LoadBalancer service publishes the RTMP port (for the cameras) and the RTSP port (for Frigate or another NVR).

## Deploy

1. Create the namespace and the refresh-token secret (see the main README for getting a token):

   ```sh
   kubectl create namespace nanit
   kubectl -n nanit create secret generic nanit-secret \
     --from-literal=refresh-token=YOUR_REFRESH_TOKEN
   ```

2. Edit `deployment.yaml`: set `NANIT_BABY_UIDS` and `NANIT_RTMP_PUBLIC_ADDR`. The address must be the service's external IP — cameras connect to it, so it has to be reachable from the camera network. Pin that IP through your load balancer (MetalLB, Cilium LB-IPAM, etc.) in `service.yaml`.

3. Apply:

   ```sh
   kubectl apply -k .
   ```

Streams come up at `rtsp://<external-ip>:8554/local/<baby-uid>`. Point your NVR there, e.g. for Frigate:

```yaml
cameras:
  nursery:
    ffmpeg:
      inputs:
        - path: rtsp://192.168.1.10:8554/local/0a1b2c3d
          roles: [detect, record]
```

## Notes

- `strategy: Recreate` and one replica are deliberate: the session PVC is RWO, and every extra controller instance consumes a Nanit "mobile app" connection slot.
- The PVC uses the cluster's default storage class; the session file is small but must survive restarts, since refresh tokens are single-use.
- Check `http://<pod>:8080/readyz` for per-camera status when something isn't streaming.
