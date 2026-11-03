# OpenWrt and WISPr NAS

**For:** operators who use OpenWrt or an AP controller as the NAS.

**Prerequisites:** gobill with the built-in RADIUS, and the NAS added under **Network > NAS**.

## Choose the NAS type

Each NAS has a **NAS type**. It decides which speed and quota attributes gobill sends.

- **MikroTik.** The default. gobill sends `Mikrotik-Rate-Limit`, `Mikrotik-Total-Limit`, and the isolir address list.
- **WISPr.** For CoovaChilli on OpenWrt and controllers that understand WISPr. gobill sends `WISPr-Bandwidth-Max-Up` and `-Down` in bits per second. The quota goes as `CoovaChilli-Max-Total-Octets` and `-Gigawords`.
- **Standard.** gobill sends RFC attributes only: login, `Session-Timeout`, `Idle-Timeout`, and Disconnect. Set the speed on the NAS. Use it for a NAS gobill does not know.

Login, validity, and disconnect work with every type.

## Limits

- Isolation works on MikroTik only. On WISPr and Standard, an isolated customer is rejected with "Account isolated, please pay to reconnect".
- WISPr has no burst. The plan burst is ignored.
- The **Test login** page and the FreeRADIUS REST mode still show MikroTik attributes.
- PPPoE on OpenWrt (`pppd` + `radius.so`) does not read speed attributes. Use Standard.
- openNDS does not use RADIUS directly.

## CoovaChilli on OpenWrt

Install the `coova-chilli` package. Set in `/etc/config/chilli`:

```
option radiusserver1 <gobill IP>
option radiusserver2 <gobill IP>
option radiussecret  <NAS secret in gobill>
option radiusnasid   <NAS name>
option coaport       3799
option uamallowed    <gobill IP>
```

- `coaport` makes Chilli accept CoA and Disconnect. FUP, boost, and the disconnect button need it.
- Chilli accepts CoA only from `radiusserver1`. If gobill sends from another IP, add `option coanoipcheck`.
- `uamallowed` opens the gobill portal and payment pages before login.

Add the OpenWrt IP under **Network > NAS** with type **WISPr**.
