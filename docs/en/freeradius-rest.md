# FreeRADIUS over REST

This document is for operators who already run FreeRADIUS, usually from PHPNuxBill, and want to keep it. gobill provides a `radius.php` endpoint that is compatible with the `rlm_rest` module.

**For:** operators

**Prerequisites:** FreeRADIUS is running with the `rest` module. gobill is installed and reachable from FreeRADIUS.

## How to use it

In `/etc/freeradius/3.0/mods-enabled/rest`, change `connect_uri` to gobill:

    connect_uri = "https://<gobill>/radius.php"

The endpoint `/radius/rest` also exists, and does the same thing.

The `authorize`, `authenticate`, `accounting`, and `post-auth` sections do not need changes. The MikroTik configuration does not need changes either. The `RadiusRest` plan from PHPNuxBill is imported as a `Radius` plan.

## Response compatibility

The response format is the same as in the old `radius.php`:

- JSON with the `control:` and `reply:` sections.
- Status 204 for a successful authenticate.
- Status 401 for a rejected request.

Auth decisions use the same logic as the built-in UDP server.

Voucher login is also supported on this endpoint. The voucher code is used as the username. The password is the same as the code, or empty. The limits are the same as on the built-in server.

## Allow-list

The old `radius.php` had no authentication. gobill keeps the endpoint closed until you open it. A request is accepted only if one of these holds:

- Its connection address is in the setting `radius_rest_allow` (IPs or CIDRs separated by commas). For FreeRADIUS on the same host, set it to `127.0.0.1, ::1`.
- It carries the header `X-Gobill-Secret` with the value of the setting `radius_rest_secret`. The compare is constant-time. A request with the right secret is accepted from any address.

If neither setting is set, every request is refused with 403, and gobill writes a warning at startup. This changed: before, an empty `radius_rest_allow` meant loopback only. After upgrading, set one of the two settings in **Settings > System & Security > FreeRADIUS**, or FreeRADIUS logins stop working.

To send the secret from FreeRADIUS 3, add the header in the `rest` call, for example in `sites-enabled/default`: `update control { REST-HTTP-Header += "X-Gobill-Secret: <the secret>" }` before `rest` in `authorize`, `post-auth` and `accounting`. Check the syntax against your FreeRADIUS version's `rlm_rest` documentation. If you cannot set a header, use `radius_rest_allow` instead.

`X-Forwarded-For` is trusted only if `trust_proxy` = `yes` and the direct connection comes from loopback or from `trusted_proxies`. The allow-list always uses the real connection address. A request that carries `X-Forwarded-For`, `X-Real-IP`, `Forwarded` or `CF-Connecting-IP` never passes the allow-list, because a reverse proxy on the same host makes every internet client look like 127.0.0.1. If FreeRADIUS reaches gobill through a proxy, use `radius_rest_secret`.

This endpoint does not use CSRF protection, because machines call it. The allow-list is its protection.

## Disconnect

The `Radius` plan does not call the router API. A forced disconnect, for example when a plan runs out or an admin presses Disconnect, is sent as a CoA Disconnect-Request directly to MikroTik.

For this to work:

1. Register the MikroTik in the NAS menu.
2. Enable `/radius incoming` on port 3799. See [MikroTik setup](mikrotik.md).

## When to choose the built-in server

| | FreeRADIUS and REST | Built-in server |
|---|---|---|
| Changes on MikroTik | None | The `/radius` address and secret are replaced |
| Components | gobill and FreeRADIUS | One process |
| Other modules, for example EAP | Yes | No |
| Auth methods | As supported by FreeRADIUS | PAP, CHAP, and MS-CHAPv2 |
| Latency | One HTTP hop per login | Direct |
| Voucher login | Yes | Yes |

Choose the built-in server for new installations. Choose FreeRADIUS for a gradual migration, or if you need another FreeRADIUS module.

If FreeRADIUS already uses UDP 1812 on the same host, set `GOBILL_RADIUS=off`. FreeRADIUS hardening is described in [security](security.md#freeradius-on-the-rest-path).

## See also

- [MikroTik setup](mikrotik.md): API mode and built-in RADIUS mode.
- [Security](security.md): RADIUS and FreeRADIUS hardening.
- [Configuration](configuration.md#network-and-proxy): `radius_rest_allow` and proxy settings.
- [Migrating from PHPNuxBill](migration-phpnuxbill.md): gradual move from the old system.
