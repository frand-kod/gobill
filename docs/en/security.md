# Security

This document covers RADIUS hardening, the firewall, application security, and 2FA for admins. The RADIUS part is done after gobill and MikroTik are working. Read the [MikroTik setup](mikrotik.md) first.

**For:** operators and developers

**Prerequisites:** gobill and the router are running. See [installation](installation.md) and [MikroTik setup](mikrotik.md).

## RADIUS hardening

### Message-Authenticator (BlastRADIUS, CVE-2024-3596)

gobill adds Message-Authenticator to all replies and CoA packets.

On MikroTik, enable it:

    /radius set [find] require-message-auth=yes-for-request-resp
    /radius print detail

In the NAS form in gobill, turn on "require Message-Authenticator". Devices that do not send this attribute are rejected. So update the NAS firmware first.

### Shared secret

- Use at least 16 random characters, unique for each NAS. Create one with `openssl rand -base64 24`.
- Do not reuse the hotspot password or an admin password.
- Change the secret on MikroTik, then match it in the NAS form:

        /radius set [find address=GOBILL-IP] secret=<NEW-SECRET>

### Firewall

**MikroTik.** Only gobill may send CoA to port 3799:

    /ip firewall filter add chain=input protocol=udp dst-port=3799 src-address=192.168.88.10 action=accept
    /ip firewall filter add chain=input protocol=udp dst-port=3799 action=drop

If `input` already has a general drop rule, put both rules above it with `place-before=<number>`. Find the number with `/ip firewall filter print`.

**gobill host, firewalld.** One rule per NAS:

    sudo firewall-cmd --permanent --add-rich-rule='rule family="ipv4" source address="192.168.88.1" port port="1812-1813" protocol="udp" accept'
    sudo firewall-cmd --permanent --add-rich-rule='rule family="ipv4" source address="192.168.1.0/24" port port="8080" protocol="tcp" accept'
    sudo firewall-cmd --reload

Do not use `--add-port=1812/udp` or `--add-port=8080/tcp`. Both open the port to all IPs.

**gobill host, nftables.** The input default is drop. Make sure SSH from the admin LAN is allowed:

    table inet gobill {
        chain input {
            type filter hook input priority 0; policy drop;
            iif lo accept
            ct state established,related accept
            ip saddr 192.168.88.1 udp dport { 1812, 1813 } accept
            ip saddr 192.168.1.0/24 tcp dport 8080 accept
            ip saddr 192.168.1.0/24 tcp dport 22 accept
        }
    }

Save it to `/etc/nftables.conf`. Check it with `sudo nft list ruleset`, then apply it with `sudo nft -f /etc/nftables.conf`. If FreeRADIUS is on another host, add `ip saddr IP-FREERADIUS tcp dport 8080 accept`.

**Limit `/radius.php`.** An empty `radius_rest_allow` means loopback only. This is the default since v0.1.4. Fill it with the FreeRADIUS IP if FreeRADIUS runs on another host. `trust_proxy` does not affect this allow-list. See [FreeRADIUS over REST](freeradius-rest.md).

### Remote link (VPS)

Do not send RADIUS UDP or CoA over the open internet.

**WireGuard (recommended, RouterOS 7).** Create keys with `wg genkey | tee privat.key | wg pubkey`. Then on MikroTik:

    /interface wireguard add name=wg-gobill listen-port=13231 private-key="<MIKROTIK-PRIVATE-KEY>"
    /ip address add address=10.10.10.2/24 interface=wg-gobill
    /interface wireguard peers add interface=wg-gobill public-key="<KUNCI-PUBLIK-VPS>" endpoint-address=<IP-VPS> endpoint-port=51820 allowed-address=10.10.10.1/32 persistent-keepalive=25s

gobill on the VPS uses `10.10.10.1`. Register that address as the RADIUS or NAS address, and use it as the `src-address` of the CoA rule. RouterOS 6 has no WireGuard. Use L2TP/IPsec, or upgrade.

**RadSec.** On RouterOS 7:

    /radius add service=hotspot,ppp address=<IP-VPS> protocol=radsec certificate=<certificate-name>

gobill does not serve RadSec natively yet. Run radsecproxy on the VPS, forwarding to `127.0.0.1:1812`, or use WireGuard alone.

### Hotspot login

`http-pap` sends the password in plain text over HTTP. Choose one of the following.

Use CHAP:

    /ip hotspot profile set [find] login-by=http-chap

Use HTTPS. The device must trust the certificate, or the captive portal shows a warning:

    /certificate import file-name=hotspot.pem passphrase=""
    /ip hotspot profile set [find] login-by=https,http-chap ssl-certificate=<certificate-name>

### PPPoE with MS-CHAPv2

MS-CHAPv2 can be cracked offline because its DES is weak. On untrusted links, such as a shared cable or open WiFi, run PPPoE inside a WireGuard tunnel. On a controlled local access network, the risk may be acceptable.

### FreeRADIUS on the REST path

Keep FreeRADIUS up to date. This matters for CVE-2019-11234 and CVE-2019-11235 (EAP-pwd), and CVE-2022-41860 and CVE-2022-41861 (EAP-SIM and AKA):

    sudo apt update && sudo apt -y upgrade freeradius
    freeradius -v

If EAP is not used, disable it:

1. Check the lines that will be commented out first:

        grep -n "eap" /etc/freeradius/3.0/sites-enabled/*

2. Disable the module and the EAP lines:

        sudo rm /etc/freeradius/3.0/mods-enabled/eap
        sudo sed -i 's/^\(\s*\)eap$/\1#eap/' /etc/freeradius/3.0/sites-enabled/*
        sudo freeradius -CX | tail -n 2

The command `freeradius -CX` must end with an OK message.

If `connect_uri` uses `https`, make sure `check_cert = yes` is set in the `tls` block of `mods-enabled/rest`. For a self-signed certificate, add the CA in `ca_file`.

## Application security

- **Passwords.** Admin and customer passwords use bcrypt. Old sha1 hashes are marked `legacy_sha1` and replaced at the first login.
- **Encrypted secrets.** Router, customer (PPPoE and hotspot), and NAS secrets use AES-GCM. The key comes from `GOBILL_SECRET_KEY` or the `.key` file. Store the key backup together with the database. See [backup and restore](backup-restore.md#key-backup-with-the-database).
- **Note.** Integration secrets, namely SMTP, Telegram, Tripay, and the metrics token, are stored as plain text in the `settings` table. This is the same as in the old application. Protect the database file and its backups.
- **CSRF.** Protected with the standard library's `http.CrossOriginProtection`. The only exceptions are the Tripay callback, which is verified by its signature, and `/radius.php`, which uses an allow-list.
- **Attempt limits.** Brute-force limits are kept in memory and reset on restart.
  - Admin and portal login: 10 failures in 15 minutes, counted per IP and per username. 2FA login uses the same limit.
  - RADIUS vouchers: 10 failures in 15 minutes per NAS and MAC, and 100 per NAS.
  - RADIUS auth per username: a separate limit for wrong passwords.
  - OTP sending: 5 times per IP in 15 minutes. Per phone number, a 60-second cooldown and at most 5 times per hour. If `notify_otp` = `no`, no OTP is sent at all.
  - Detected brute force also triggers an operator alert. See [monitoring](monitoring.md).
- **Forgotten password.** The reply is the same whether or not the username exists. It cannot be used to guess accounts.
- **Customer passwords.** At least 8 characters and at most 35. They apply to registration, change, reset, and edits by an admin.
- **First admin password.** It is random, 16 characters, and written to `initial-admin-password.txt` in the database folder, with mode 0600. It is not written to the log. Delete the file after you log in.
- **Proxy.** `X-Forwarded-For` is read only if the direct connection comes from loopback, or from `trusted_proxies` with `trust_proxy` = `yes`. The login limiter uses that client address. The `/radius.php` allow-list always uses the real connection address, so headers from outside cannot fake the IP.
- **Voucher printing.** Only for staff roles: SuperAdmin, Admin, Agent, and Sales.
- **Public endpoints.** `/health` shows only the database and disk status. `/metrics` requires a bearer token, and returns 404 if the token is turned off.
- **Sessions.** The cookie uses `HttpOnly`, `SameSite=Lax`, and `Secure` by default. `Secure` is turned off only with `GOBILL_HTTPS=0`. Sessions are revoked when the password, role, or status changes.
- **Idle timeout.** Set by `session_timeout_duration`, in minutes, default 120. The setting `single_session` = `yes` limits admins to one active session.
- **Roles.** Checked in middleware for each route. An admin cannot promote someone to SuperAdmin. The last SuperAdmin is protected.
- **SQL.** All queries go through `sqlc` with bound parameters.
- **Logs.** Notification errors are filtered, so tokens and API keys do not leak.
- **Database dumps.** The files `docs/*.sql` and `*.sql.gz` are in `.gitignore` and must not be committed. Do not put real credentials in documents or tests. Use placeholders such as `<SECRET>`.

## Two-step verification (2FA) for admins

2FA is optional for each admin account. Customers do not use 2FA. After the correct password, login asks for a 6-digit code from an authenticator app, such as Google Authenticator or Authy. A code is valid for 30 seconds and is accepted with a tolerance of one step, that is, ±30 seconds.

It is recommended for every SuperAdmin. A SuperAdmin account can change all settings and users.

### Enabling

1. Log in, open **Change Password**, then **Manage 2FA**. You can also open `/admin/2fa` directly.
2. Click **Enable 2FA**.
3. Scan the QR code with an authenticator app, or type the key shown below it.
4. Enter the 6-digit code from the app. 2FA becomes active only after this code is correct.
5. Save the 8 **recovery codes** that appear. The codes are shown only once.

### Recovery codes

- They have the form `XXXX-XXXX`. Each code can be used once, and is stored as a bcrypt hash.
- They are used on the second login screen if the authenticator app is not available. Type them with or without the dash.
- After a code is used, it is dead. If all are used, disable and enable 2FA again to get 8 new codes.

### Disabling

On `/admin/2fa`, enter the current password and one 6-digit code from the app. Remaining recovery codes are deleted too.

### Reset by a SuperAdmin

If an admin loses the app and the recovery codes:

1. A SuperAdmin opens **Admin Users**.
2. The SuperAdmin selects the account, then clicks **Reset 2FA**.

That admin can log in with the password only. 2FA must be enabled again. The action is recorded in the activity log as `users.2fa.reset`. A reset does not ask for the SuperAdmin's password. So protect your own SuperAdmin account with 2FA.

### Limits

- Wrong code attempts count toward the login limit: 10 failures in 15 minutes per IP and per username.
- A used code cannot be used again. The list of used codes is kept in memory. After a restart, the same code may be accepted again for the rest of its time step, at most about 90 seconds.
- Customer login and the API do not use admin 2FA. Admin sessions are opened only through the login page.

## See also

- [MikroTik setup](mikrotik.md): RADIUS settings on the router.
- [FreeRADIUS over REST](freeradius-rest.md): the REST path and allow-list.
- [Configuration](configuration.md#network-and-proxy): proxy and allow-list settings.
- [Monitoring](monitoring.md): brute-force alerts and the `/metrics` endpoint.
- [Backup and restore](backup-restore.md): key and database backups.
