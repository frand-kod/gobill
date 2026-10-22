# MikroTik setup

   **A router behind NAT or CGNAT cannot receive CoA.** gobill sends CoA to the source IP of the RADIUS packets, on port 3799. If the router goes out through NAT, for example an ISP modem or CGNAT, the packet never arrives. Login and accounting still work, but Disconnect and forced isolation fail. The session only ends when the customer reconnects. The fix is a tunnel between the router and gobill, see [remote link](security.md#remote-link-vps).
This document explains how to connect a MikroTik router to gobill. There are two modes, and both can be used together:

- **API mode.** gobill manages the router through the RouterOS API.
- **Built-in RADIUS mode.** The router asks the gobill RADIUS server.

This setup was tested on RouterOS 6.49.22. Hardening is described in [security](security.md).

**For:** operators

**Prerequisites:** gobill is running. Replace `192.168.88.10` with the IP of the gobill host, and `<SECRET>` with the same RADIUS secret on both sides.

## API mode

gobill calls the RouterOS API to create hotspot users, create PPPoE secrets, create queues, and disconnect sessions. The port is TCP 8728, or 8729 if TLS is used.

Create a dedicated user on MikroTik:

    /user group add name=gobill policy=read,write,api,test
    /user add name=gobill group=gobill password=<STRONG-PASSWORD>

In the gobill UI, add the router with:

- the MikroTik IP;
- the user `gobill`;
- the password from above.

Then sync the plans to the router profiles.

## Built-in RADIUS mode

### RADIUS client for hotspot and PPPoE

    /radius add service=hotspot,ppp address=192.168.88.10 secret=<SECRET> authentication-port=1812 accounting-port=1813 timeout=2s
    /radius incoming set accept=yes port=3799

`/radius incoming` opens CoA on port 3799. gobill uses CoA to disconnect sessions or change plans, without waiting for the customer to reconnect. When a customer's Radius plan changes (recharge, plan change, or an admin edits the plan or bandwidth), gobill sends a CoA-Request with the new `Mikrotik-Rate-Limit` and `Session-Timeout` to the online sessions. If the NAS refuses (NAK) or does not answer, gobill disconnects the session with a Disconnect-Request and the customer gets the new plan at the next login.

Set the interim interval on the router (the examples below use `1m`). gobill derives the stale-session limit per NAS from the observed Interim-Update spacing (about 2x the interval + 1 minute). `GOBILL_STALE_AFTER` stays the lower bound. Without interim updates, a session is stale after `GOBILL_STALE_AFTER`.

### Hotspot

    /ip hotspot profile set [find] use-radius=yes radius-accounting=yes radius-interim-update=1m

### PPPoE

    /ppp aaa set use-radius=yes accounting=yes interim-update=1m

### Register the NAS in gobill

In the NAS menu, fill in:

- the router IP;
- the same secret;
- the Message-Authenticator mode. Keep **Auto (recommended)**. If the router is older than RouterOS 6.49.18 or 7.15, choose **Not required (legacy NAS)**.

The `address` on `/radius` must match the gobill host IP that the router can reach. Without a NAS row, packets from the router are dropped and logged. Disconnects then only produce a warning.

Voucher login on the hotspot page uses the code as the username. This login goes directly through RADIUS. Failed attempts are limited to 10 per 15 minutes per NAS and MAC, and 100 per 15 minutes per NAS.

## Lessons from field testing

1. **The gobill host must not be a hotspot client.** Universal NAT on the hotspot makes the router reach that host through `to-address`, not its real IP. As a result, RADIUS and CoA fail. The fix is to bypass the host:

        /ip hotspot ip-binding add mac-address=<MAC-HOST-GOBILL> type=bypassed

   Another option is to place the host on a separate port or VLAN.

2. **RouterOS 6.49.18+ / 7.15+ and `require-message-auth`.** gobill adds Message-Authenticator to all replies. On these versions, set it on the router so it checks them:

        /radius set [find] require-message-auth=yes-for-request-resp

   Versions before 6.49.18 and 7.15 may not send Message-Authenticator in requests. For them, set the NAS to **Not required (legacy NAS)** in gobill (see [security](security.md#radius-hardening)).

3. **NAS-IP-Address and source IP for CoA.** The router matches CoA against its own NAS-IP-Address. CoA with the wrong identity is rejected (NAK). gobill sends the NAS-IP-Address that the NAS reports itself, and decodes Error-Cause in the log. Make sure the NAS IP in gobill matches the IP the router uses as the source of RADIUS packets. If the router has several IPs, set `src-address` on `/radius`.

4. **Safe testing on a production router.** Do not change the profiles that customers use. Create a separate `/radius` entry and a test profile that uses `domain=` with `split-user-domain=yes`:

        /radius add service=hotspot address=192.168.88.10 secret=<SECRET> domain=uji.local comment=gobill-test

   Only logins as `user@uji.local` are sent to gobill. Other customers keep using the old server.

   When you are done:

   1. Set `split-user-domain=no` again.
   2. Delete the test `/radius` entry.
   3. Delete the test profile and test user.

## Isolir

Isolated customers get full isolation except an allow-list. Their web requests are redirected to the isolir page, so the phone's captive-portal check opens it. Every other app and site is blocked. The hosts in **Allowed while isolated** stay reachable: WhatsApp by default, so the customer can contact you, and a bank app if you add it. **Settings > Isolation**, section **Setup router**, shows the rules as one copyable block, with your gobill address, address-list name and allow-list filled in.

Paste the whole block at once, in order. The rules must sit above any general `accept` rule in the forward chain, or isolated clients are not cut off. The block has this shape (the list name is `isolir`, the allow-list is `isolir-allow`):

    /ip firewall nat add chain=dstnat src-address-list=isolir dst-address-type=!local protocol=tcp dst-port=80 action=dst-nat to-addresses=192.168.99.2 to-ports=8090
    /ip firewall filter add chain=forward src-address-list=isolir dst-address=192.168.99.2 action=accept
    /ip firewall address-list add list=isolir-allow address=wa.me comment="gobill isolir allow"
    /ip firewall filter add chain=forward src-address-list=isolir protocol=tcp dst-port=443 tls-host=*.whatsapp.net action=add-dst-to-address-list address-list=isolir-allow address-list-timeout=1d comment="isolir: learn *.whatsapp.net"
    /ip firewall filter add chain=forward src-address-list=isolir protocol=tcp dst-port=443 tls-host=wa.me action=add-dst-to-address-list address-list=isolir-allow address-list-timeout=1d comment="isolir: learn wa.me"
    /ip firewall filter add chain=forward src-address-list=isolir protocol=tcp dst-port=443 tcp-flags=syn action=accept comment="isolir: TLS handshake"
    /ip firewall filter add chain=forward src-address-list=isolir dst-address-list=isolir-allow action=accept comment="isolir: allowed apps"
    /ip firewall filter add chain=forward src-address-list=isolir action=drop comment="isolir: block the rest"

The rules do this, in order:

1. **NAT.** HTTP port 80 from isolated clients goes to gobill. `dst-address-type=!local` leaves traffic to the router itself alone, so the hotspot login and logout pages stay reachable, for example `http://192.168.20.1/logout`.
2. **gobill accept.** Isolated clients may reach gobill.
3. **Allow-list.** Each exact host (no `*.`) is added to the `isolir-allow` address-list. Every entry, wildcard or not, gets a `tls-host` rule: when an isolated client opens a TLS connection whose SNI matches the entry, the destination IP is added to `isolir-allow` for one day.
4. **TLS handshake.** New HTTPS connections (SYN) are accepted, so the handshake runs and the rule above can learn the IP.
5. **Allowed apps.** Traffic to an address in `isolir-allow` is accepted.
6. **Block the rest.** Everything else from isolated clients is dropped.

The list name is the **MikroTik address-list name** setting (default `isolir`). The allow-list is that name with `-allow` added. The allow-list is set in **Allowed while isolated**, one host per line. Wildcards such as `*.whatsapp.net` are allowed. After you change it, paste the block again.

### RouterOS 6 limitation

- DNS entries in an address-list resolve exact names only on RouterOS 6. That is why exact hosts are added to the list up front, and wildcard hosts are learned instead.
- A wildcard host is learned when the app opens a TLS connection to it. The learned IPs last one day. An app that connects to a new CDN host gets through once the rule has learned that host.
- So WhatsApp media from CDN hosts that change may sometimes fail. Messages and calls to the admin still work.

### Adding a bank app

Find the host the app connects to, for example with the router's connection list or the app's support notes. Add it on its own line in **Allowed while isolated**, such as `*.bank.example` for all its hosts or `m.bank.example` for one. Save, then paste the block again. Test from an isolated client that the app works, then check that other sites stay blocked.

Two more items are outside the forward chain:

    /ip hotspot walled-garden ip add dst-address=192.168.99.2 dst-port=8090 action=accept
    /ip hotspot ip-binding add mac-address=<MAC-HOST-GOBILL> type=bypassed

The walled garden lets the isolir page open before hotspot login. The address and port are the ones gobill listens on, set by `GOBILL_HTTP` (see [configuration](configuration.md)). For example, `GOBILL_HTTP=192.168.99.2:8090` gives `to-addresses=192.168.99.2 to-ports=8090`. If `GOBILL_HTTP` is empty or `0.0.0.0`, gobill uses the first LAN IPv4 address of its host. Set `GOBILL_HTTP` to a fixed LAN IP when the host has more than one address.

Replace `<MAC-HOST-GOBILL>` with the MAC address of gobill's LAN card. Without the bypass, the hotspot can capture gobill's own traffic.

### Testing the isolir page

gobill serves the isolir page to an isolated client at `/` and at every other path, with any `Host` header. Test from a LAN client with curl:

    curl -i -H "Host: example.com" http://192.168.99.2:8090/

Do not test with `/tool fetch` on RouterOS. It sends a second `Host` header and gets `400 Bad Request`.

## See also

- [Security](security.md#radius-hardening): RADIUS hardening, firewall, and CoA.
- [FreeRADIUS over REST](freeradius-rest.md): the option if you use FreeRADIUS.
- [Migrating from PHPNuxBill](migration-phpnuxbill.md): move from the old system.
