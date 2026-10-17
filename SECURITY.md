# Security policy

gobill handles customer data, payments and network access for ISPs. Please report security
problems privately so they can be fixed before they are public.

## Supported versions

Only the latest release gets security fixes. gobill is pre-1.0, so update with `sudo gobill update`
instead of waiting for a backport.

| Version | Supported |
|---|---|
| Latest `v0.x` release | Yes |
| Older releases | No |

## Reporting a vulnerability

Use GitHub's private reporting: open the repository's **Security** tab and choose
**Report a vulnerability**. Do not open a public issue, discussion or pull request.

Please include:

- the gobill version (`gobill --version`) and how it is deployed (systemd, Docker, behind a proxy),
- the steps to reproduce, and what an attacker gains,
- whether the problem is already public.

What to expect:

- an acknowledgement within 3 working days,
- an assessment within 10 working days,
- a fixed release as soon as possible, usually within 30 days for confirmed issues. You are
  credited in the CHANGELOG unless you ask not to be.

## Scope

In scope: the gobill binary, the admin UI, the customer portal, the RADIUS server and
`/radius.php`, payment callbacks, `gobill update`, and `deploy/install.sh`.

Out of scope: misconfiguration the documentation warns against (for example `GOBILL_HTTPS=0` on a
public address, or `GOBILL_PPROF` bound to `0.0.0.0`), and problems in MikroTik, FreeRADIUS or
payment gateways themselves.

## Release signing

Every release from v0.1.18 includes `SHA256SUMS.sig`, an ed25519 signature of `SHA256SUMS`.
`gobill update` checks it with the public key built into the binary
(`internal/update/release.pub`) and refuses releases that are unsigned or signed by another key.

To check a release by hand:

    go run ./tools/releasesign verify path/to/SHA256SUMS   # SHA256SUMS.sig must be next to it
    sha256sum --check --ignore-missing SHA256SUMS

The private key is kept offline by the maintainer and is never stored in GitHub. CI builds a
draft release; the maintainer signs `SHA256SUMS` locally and publishes it with `make sign`.
