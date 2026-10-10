# MikroTik setup

This document explains how to connect a MikroTik router to NuxBill. There are two modes, and both can be used together:

- **API mode.** NuxBill manages the router through the RouterOS API.
- **Built-in RADIUS mode.** The router asks the NuxBill RADIUS server.

This setup was tested on RouterOS 6.49.22. Hardening is described in [security](security.md).

**For:** operators

**Prerequisites:** NuxBill is running. Replace `192.168.88.10` with the IP of the NuxBill host, and `<SECRET>` with the same RADIUS secret on both sides.

## API mode

NuxBill calls the RouterOS API to create hotspot users, create PPPoE secrets, create queues, and disconnect sessions. The port is TCP 8728, or 8729 if TLS is used.

Create a dedicated user on MikroTik:

    /user group add name=nuxbill policy=read,write,api,test
    /user add name=nuxbill group=nuxbill password=<STRONG-PASSWORD>

In the NuxBill UI, add the router with:

- the MikroTik IP;
- the user `nuxbill`;
- the password from above.

Then sync the plans to the router profiles.

## Built-in RADIUS mode

### RADIUS client for hotspot and PPPoE

    /radius add service=hotspot,ppp address=192.168.88.10 secret=<SECRET> authentication-port=1812 accounting-port=1813 timeout=2s
    /radius incoming set accept=yes port=3799

`/radius incoming` opens CoA on port 3799. NuxBill uses CoA to disconnect sessions or change plans, without waiting for the customer to reconnect.

### Hotspot

    /ip hotspot profile set [find] use-radius=yes radius-accounting=yes interim-update=1m

### PPPoE

    /ppp aaa set use-radius=yes accounting=yes interim-update=1m

### Register the NAS in NuxBill

In the NAS menu, fill in:

- the router IP;
- the same secret;
- the option "require Message-Authenticator", if the router supports it.

The `address` on `/radius` must match the NuxBill host IP that the router can reach. Without a NAS row, packets from the router are dropped and logged. Disconnects then only produce a warning.

Voucher login on the hotspot page uses the code as the username. This login goes directly through RADIUS. Failed attempts are limited to 10 per 15 minutes per NAS and MAC, and 100 per 15 minutes per NAS.

## Lessons from field testing

1. **The NuxBill host must not be a hotspot client.** Universal NAT on the hotspot makes the router reach that host through `to-address`, not its real IP. As a result, RADIUS and CoA fail. The fix is to bypass the host:

        /ip hotspot ip-binding add mac-address=<MAC-HOST-NUXBILL> type=bypassed

   Another option is to place the host on a separate port or VLAN.

2. **RouterOS 6.49 and `require-message-auth`.** NuxBill adds Message-Authenticator to all replies. The router accepts the value `yes-for-request-resp`:

        /radius set [find] require-message-auth=yes-for-request-resp

3. **NAS-IP-Address and source IP for CoA.** The router matches CoA against its own NAS-IP-Address. CoA with the wrong identity is rejected (NAK). NuxBill now sends the NAS-IP-Address that the NAS reports itself, and decodes Error-Cause in the log. Make sure the NAS IP in NuxBill matches the IP the router uses as the source of RADIUS packets. If the router has several IPs, set `src-address` on `/radius`.

4. **Safe testing on a production router.** Do not change the profiles that customers use. Create a separate `/radius` entry and a test profile that uses `domain=` with `split-user-domain=yes`:

        /radius add service=hotspot address=192.168.88.10 secret=<SECRET> domain=uji.local comment=nuxbill-test

   Only logins as `user@uji.local` are sent to NuxBill. Other customers keep using the old server.

   When you are done:

   1. Set `split-user-domain=no` again.
   2. Delete the test `/radius` entry.
   3. Delete the test profile and test user.

## See also

- [Security](security.md#radius-hardening): RADIUS hardening, firewall, and CoA.
- [FreeRADIUS over REST](freeradius-rest.md): the option if you use FreeRADIUS.
- [Migrating from PHPNuxBill](migration-phpnuxbill.md): move from the old system.
