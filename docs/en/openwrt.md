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

Tested with OpenWrt 25.12.6 and CoovaChilli 1.7.

1. Install the packages. OpenWrt 25 uses nftables, and the Chilli scripts call `iptables`, so install `iptables-nft` too:

   ```
   apk add coova-chilli iptables-nft
   ```

2. Hand the LAN to Chilli. The LAN has no IP, and OpenWrt DHCP on the LAN is off. Chilli gives clients their IP.

   ```
   uci set network.lan.proto=none
   uci delete network.lan.ipaddr
   uci set dhcp.lan.ignore=1
   uci add_list firewall.@zone[0].device=tun0
   uci commit
   ```

   The `lan` zone must hold `tun0`. Without it, a logged-in client cannot reach the internet.

3. Set `/etc/config/chilli`:

   ```
   config chilli
       option disabled      0
       option dhcpif        br-lan
       option net           10.1.0.0/24
       option uamlisten     10.1.0.1
       option uamserver     <login page URL>
       option uamsecret     <UAM secret>
       option radiusserver1 <gobill IP>
       option radiusserver2 <gobill IP>
       option radiussecret  <NAS secret in gobill>
       option radiusnasid   <NAS name>
       option coaport       3799
       option uamallowed    <gobill IP>
   ```

   - `coaport` makes Chilli accept CoA and Disconnect. FUP, boost, and the disconnect button need it.
   - Chilli accepts CoA only from `radiusserver1`. If gobill sends from another IP, add `option coanoipcheck 1`.
   - `uamallowed` opens the gobill portal and payment pages before login.

4. Run `/etc/init.d/network reload`, then `/etc/init.d/chilli enable` and `/etc/init.d/chilli restart`.

Add the OpenWrt IP under **Network > NAS** with type **WISPr**. Check sessions with `chilli_query list`. The last two columns before the URL are the up and down limits in bits per second.

## Lab results

- Login, accounting, Disconnect, and isolation work.
- CoA changes the speed without dropping the session.
- Quota: Chilli ends the session once `CoovaChilli-Max-Total-Octets` is used.
- Speed: Chilli limits by dropping packets. A TCP download gets about 60 to 95 percent of the limit, and sometimes stalls for a few seconds. This is Chilli behaviour, not gobill.
