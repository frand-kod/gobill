# MikroTik setup

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

`/radius incoming` opens CoA on port 3799. gobill uses CoA to disconnect sessions or change plans, without waiting for the customer to reconnect.

### Hotspot

    /ip hotspot profile set [find] use-radius=yes radius-accounting=yes interim-update=1m

### PPPoE

    /ppp aaa set use-radius=yes accounting=yes interim-update=1m

### Register the NAS in gobill

In the NAS menu, fill in:

- the router IP;
- the same secret;
- the Message-Authenticator mode. Keep "Auto" unless the router is older than RouterOS 6.49.18 (or 7.15). Then choose "Not required".

The `address` on `/radius` must match the gobill host IP that the router can reach. Without a NAS row, packets from the router are dropped and logged. Disconnects then only produce a warning.

Voucher login on the hotspot page uses the code as the username. This login goes directly through RADIUS. Failed attempts are limited to 10 per 15 minutes per NAS and MAC, and 100 per 15 minutes per NAS.

## Lessons from field testing

1. **The gobill host must not be a hotspot client.** Universal NAT on the hotspot makes the router reach that host through `to-address`, not its real IP. As a result, RADIUS and CoA fail. The fix is to bypass the host:

        /ip hotspot ip-binding add mac-address=<MAC-HOST-GOBILL> type=bypassed

   Another option is to place the host on a separate port or VLAN.

2. **RouterOS 6.49.18+ / 7.15+ and `require-message-auth`.** gobill adds Message-Authenticator to all replies. On these versions, set it on the router so it checks them:

        /radius set [find] require-message-auth=yes-for-request-resp

   Versions before 6.49.18 and 7.15 may not send Message-Authenticator in requests. For them, set the NAS to "Not required" in gobill (see [security.md](security.md)).

3. **NAS-IP-Address and source IP for CoA.** The router matches CoA against its own NAS-IP-Address. CoA with the wrong identity is rejected (NAK). gobill now sends the NAS-IP-Address that the NAS reports itself, and decodes Error-Cause in the log. Make sure the NAS IP in gobill matches the IP the router uses as the source of RADIUS packets. If the router has several IPs, set `src-address` on `/radius`.

4. **Safe testing on a production router.** Do not change the profiles that customers use. Create a separate `/radius` entry and a test profile that uses `domain=` with `split-user-domain=yes`:

        /radius add service=hotspot address=192.168.88.10 secret=<SECRET> domain=uji.local comment=gobill-test

   Only logins as `user@uji.local` are sent to gobill. Other customers keep using the old server.

   When you are done:

   1. Set `split-user-domain=no` again.
   2. Delete the test `/radius` entry.
   3. Delete the test profile and test user.

## Isolir

Isolir sends the HTTP traffic of isolated customers to gobill, so they see the isolir page instead of other sites. **Settings > Isolir > Setup router** shows the commands with your gobill address filled in. This section gives the rules behind them.

The rules apply in this order on the router. The `accept` rule must come before the `drop` rule in the forward chain:

1. **NAT.** Redirect HTTP port 80 from the isolir address-list to gobill.
2. **Accept.** Let isolated clients reach gobill.
3. **Optional drop.** Block all other traffic from isolated clients. This is full isolation.
4. **Walled garden.** Let the isolir page open before hotspot login.
5. **Hotspot bypass.** Keep gobill's own traffic out of the hotspot.

    /ip firewall nat add chain=dstnat src-address-list=isolir protocol=tcp dst-port=80 action=dst-nat to-addresses=192.168.99.2 to-ports=8090
    /ip firewall filter add chain=forward src-address-list=isolir dst-address=192.168.99.2 action=accept
    /ip firewall filter add chain=forward src-address-list=isolir action=drop
    /ip hotspot walled-garden ip add dst-address=192.168.99.2 dst-port=8090 action=accept
    /ip hotspot ip-binding add mac-address=<MAC-HOST-GOBILL> type=bypassed

The address and port are the ones gobill listens on. They are set by `GOBILL_HTTP`, see [configuration](configuration.md). For example, `GOBILL_HTTP=192.168.99.2:8090` gives `to-addresses=192.168.99.2 to-ports=8090`. If `GOBILL_HTTP` is empty or `0.0.0.0`, gobill uses the first LAN IPv4 address of its host. Set `GOBILL_HTTP` to a fixed LAN IP when the host has more than one address.

Replace `<MAC-HOST-GOBILL>` with the MAC address of gobill's LAN card. Without the bypass, the hotspot can capture gobill's own traffic.

Only HTTP on port 80 is redirected to the isolir page. HTTPS and apps still work at the isolir speed. Add the optional drop rule (step 3) only for full isolation. It also blocks WhatsApp and bank apps, so edit the default isolir message too.

### Testing the isolir page

gobill serves the isolir page to an isolated client at `/` and at every other path, with any `Host` header. Test from a LAN client with curl:

    curl -i -H "Host: example.com" http://192.168.99.2:8090/

Do not test with `/tool fetch` on RouterOS. It sends a second `Host` header and gets `400 Bad Request`.

## See also

- [Security](security.md#radius-hardening): RADIUS hardening, firewall, and CoA.
- [FreeRADIUS over REST](freeradius-rest.md): the option if you use FreeRADIUS.
- [Migrating from PHPNuxBill](migration-phpnuxbill.md): move from the old system.
