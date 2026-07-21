# Security policy

Security fixes are made on the default branch and released in the next patch
version. Older image tags are not maintained independently.

## Reporting a vulnerability

Use the repository's private vulnerability-reporting form when it is available:

<https://github.com/keatsfonam/nanit-controller/security/advisories/new>

If private reporting is unavailable, open a minimal issue asking the maintainer
for a private contact channel. Do not include exploit details, Nanit tokens,
session files, baby/camera identifiers, private stream URLs, credentials, or
video samples in a public issue.

Include affected versions, impact, reproduction conditions, and a proposed
embargo window. This project is maintained without a response-time SLA.

## Sensitive data

Treat all of the following as secrets:

- Nanit refresh and access tokens;
- `session.json` and recovery copies;
- RTSP reader credentials;
- unredacted Nanit API responses; and
- camera stream URLs and recordings.

If a token or session file is disclosed, stop every controller using it and
complete a fresh 2FA bootstrap. Rotated refresh tokens cannot be recovered from
an older backup.
