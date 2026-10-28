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

The NAS form in gobill has a "Message-Authenticator" setting, one of three modes:

- **Auto (recommended).** Access-Requests without Message-Authenticator are accepted until the NAS has sent one valid Message-Authenticator. From then on, requests without it are dropped and logged. The NAS list shows "MA: not seen yet" or "MA: seen".
- **Always required.** Requests without a valid Message-Authenticator are dropped from the start. Use it only when the NAS always sends one.
- **Not required (legacy NAS).** Requests without Message-Authenticator are accepted. Use it for old firmware that never sends one. Invalid or duplicate attributes are still rejected.

A Message-Authenticator that is present but wrong, malformed, or repeated is always rejected, in every mode.

RouterOS 6.49.18 and later, and 7.15 and later, send Message-Authenticator in Access-Request. Older versions may not, so use "Not required" for them. To make the router check the replies gobill sends, run on the router:

    /radius set [find] require-message-auth=yes-for-request-resp

The RADIUS REST endpoint `/radius.php` is not affected by these modes. FreeRADIUS in front of it does its own check.

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

**Limit `/radius.php`.** An empty `radius_rest_allow` means loopback only. Fill it with the FreeRADIUS IP if FreeRADIUS runs on another host. `trust_proxy` does not affect this allow-list. While it is empty, a request that carries a proxy header (`X-Forwarded-For`, `X-Real-IP`, `Forwarded`, `CF-Connecting-IP`) is refused, because behind Nginx or cloudflared on the same host every internet client arrives from loopback. See [FreeRADIUS over REST](freeradius-rest.md).

### Remote link (VPS)

Do not send RADIUS UDP or CoA over the open internet.

**Built-in WireGuard (recommended, RouterOS 7).** gobill carries a userspace WireGuard server inside the binary: no root, no `/dev/net/tun`, no host package, and it works in Docker. The router opens the connection to gobill, so a router behind NAT or CGNAT can be reached. RADIUS (1812 and 1813) is served inside the tunnel, and CoA and the RouterOS API are sent through it.

It is off by default.

1. Open **Settings → WireGuard** (SuperAdmin only). Turn it on, set the UDP port (default 51820), the public endpoint (a domain or public IP that points straight at this server, without a port) and the tunnel subnet (default `10.99.0.0/24`). The Status card shows the public address and UDP check.
2. Open the router page (**Routers → Edit**), section **WireGuard tunnel**, and click **Provision**. gobill makes a keypair for the router (the private key is stored encrypted through `internal/secret`, never as plain text in the database), takes one IP from the subnet, and creates a NAS row with its own RADIUS secret for that router.
3. gobill shows **one line** like `/tool fetch url="https://gobill.example.com/provision/<token>" dst-path=gobill.rsc; /import gobill.rsc`. Paste it into the router terminal. The private key, the RADIUS secret and the API user password are not in the line: the router fetches them from gobill with the token, so no secret goes through the clipboard. The token is 256 random bits, only its hash is stored, it is valid for 15 minutes and **works once**. A second use is refused (HTTP 410) from any address, and so is an expired or wrong token. The router page shows "script fetched from IP X at time Y" and warns when there is no handshake after the token was used (someone else may have used it: provision again). The web address in the line is the one you used in the browser, so the router must be able to reach it (HTTPS is advised; the default `/tool fetch` does not check the certificate).
4. The script is safe to run again. It checks the RouterOS version and stops with a clear message on RouterOS 6, then removes what an earlier run made (the `wg-gobill` interface, the `gobill` user and `gobill-api` group, and the `/radius` entry and firewall rules tagged `gobill`) before adding them again. It adds the WireGuard interface and peer, an NTP client, `/radius` with `src-address` set to the tunnel address, firewall rules for CoA and the API from gobill's tunnel address only (`place-before=0`), the API user `gobill` in the restricted group `gobill-api` (policy `api,read,write,test`, no ssh, ftp, winbox, web or policy) that may only log in from gobill's tunnel address, and restricts the `api` service to that address. gobill stores the API user's password (encrypted) and sets the router **Host** to the tunnel address itself.
5. Click **Check tunnel**. The result shows four stages, each with its own message: handshake, API login, RADIUS packet and CoA. "The handshake never happened" is told apart from "the API login failed".

Buttons on the router page: **Provision again** (always makes a new key, a new API password and a new token; the old key and token stop working at once), **Reset peer session**, and **Revoke** (the peer and its NAS row are removed at once and the API password gobill holds is replaced; the `gobill` user on the router can only log in from the tunnel address, which is now cut). Deleting a router revokes its peer too. gobill does not log in to the router to delete the `gobill` user; if you retire the router, remove that user and the `wg-gobill` interface on it yourself.

**RouterOS 6.** RouterOS 6 has no WireGuard and the provisioning script stops there. Use the alternative: an L2TP/IPsec or SSTP tunnel from the router to the gobill server (or to a VPN that reaches it), then set the router **Host** and the NAS to the address inside that tunnel. This is set up by hand and gobill does not automate it. Or upgrade to RouterOS 7.

Safeguards:

- `GOBILL_WG=off` in the environment is the kill-switch and beats the setting in the UI. A build with the `nowg` tag (`go build -tags nowg ./cmd/gobill`) removes the feature from the binary. Releases are one variant, with WireGuard.
- Each peer has a single `AllowedIPs` entry (its tunnel address), there is no routing between peers, and on the tunnel side gobill listens on 1812 and 1813 only. The admin HTTP is not opened to the tunnel.
- The RADIUS secret is per router. A stolen router or a leaked config is dealt with by revoking it.
- After a restart gobill starts the handshake itself, to the last address of each router (it is stored), so the tunnel does not stay down for about 2 minutes waiting for the router to try again.

Changing the port, endpoint or subnet cuts every router that is provisioned. The settings page shows how many routers are affected and asks for confirmation. After saving, those routers are marked "needs provisioning again" and their provisioning line must be pasted again. Switching WireGuard off (or `GOBILL_WG=off` while routers still use it) cuts them too; gobill logs the count at start.

Risks to know:

- **When gobill is down, every new login fails.** Running sessions stay up, but reconnects and new vouchers are refused until gobill is back. If that is not acceptable, leave the local secrets and users on the router active as a fallback. Isolation control becomes weaker, and the choice is the admin's. The "NAS silent" alert in Settings → Notifications tells you when a router that is normally active stops sending RADIUS requests.
- **The router clock goes back after a reboot.** WireGuard refuses a handshake whose timestamp is older than the last one. A MikroTik without an RTC (hAP series and similar) returns to its initial clock after a power cut. The provisioning script sets an NTP client. If the tunnel still does not come back, click **Reset peer session**.
- **gobill cannot test UDP from inside.** The Status card can only confirm that UDP is open once a router completes a handshake. Cloudflare Tunnel (cloudflared) and proxied Cloudflare records do not forward UDP: use a DNS-only record that points at the server's public IP. A server with no public IP at all (CGNAT) cannot be used. If the operator blocks 51820, change the port (for example 443/udp).
- The server's WireGuard key is in the settings (`wg_private`, encrypted) and goes into `gobill backup`. The encryption key `gobill.db.key` must still be kept separately, see [backup and restore](backup-restore.md).
- Cost: the binary is about 4.6 MB larger (23.9 MB to 28.5 MB on amd64, +19%). Idle RAM is about 3.5 MB higher with WireGuard off, and about 3 MB more when the tunnel is on (200 peers: about 5 MB more than off). Measured without traffic.

**Manual WireGuard (alternative).** If the built-in WireGuard is off, or gobill uses a tunnel on the host. Create keys with `wg genkey | tee privat.key | wg pubkey`. Then on MikroTik:

    /interface wireguard add name=wg-gobill listen-port=13231 private-key="<MIKROTIK-PRIVATE-KEY>"
    /ip address add address=10.10.10.2/24 interface=wg-gobill
    /interface wireguard peers add interface=wg-gobill public-key="<KUNCI-PUBLIK-VPS>" endpoint-address=<IP-VPS> endpoint-port=51820 allowed-address=10.10.10.1/32 persistent-keepalive=25s

gobill on the VPS uses `10.10.10.1`. Register that address as the RADIUS or NAS address, and use it as the `src-address` of the CoA rule and of `/radius` on the router.

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
- **Encrypted secrets.** Router, customer (PPPoE and hotspot), and NAS secrets, plus the secret settings (Telegram token, SMTP password, webhook secret, GOWA password, Tripay and Duitku keys, metrics token), use AES-GCM. Settings saved by an older version are encrypted at the next start. The key comes from `GOBILL_SECRET_KEY` or the `.key` file. Store the key backup together with the database. See [backup and restore](backup-restore.md#key-backup-with-the-database).
- **Note.** Integration secrets, namely SMTP, Telegram, Tripay, and the metrics token, are stored as plain text in the `settings` table. This is the same as in the old application. Protect the database file and its backups.
- **CSRF.** Protected with the standard library's `http.CrossOriginProtection`. The only exceptions are the Tripay callback, which is verified by its signature, and `/radius.php`, which uses an allow-list.
- **Security headers.** Every response sends `X-Frame-Options: DENY` against clickjacking, `X-Content-Type-Options: nosniff`, and `Referrer-Policy: same-origin`, so a QRIS payment link with its token is never sent to another site.
- **Attempt limits.** Brute-force limits are kept in memory and reset on restart.
  - Admin and portal login: 10 failures in 15 minutes lock that IP. A single username is only slowed (0.25 s added per failure, up to 3 s), never locked, so nobody else can lock an admin account. 2FA login uses the same limit.
  - RADIUS vouchers: 10 failures in 15 minutes per NAS and MAC, and 100 per NAS.
  - RADIUS auth per username: a separate limit for wrong passwords.
  - OTP sending: 5 times per IP in 15 minutes. Per phone number, a 60-second cooldown and at most 5 times per hour. If `notify_otp` = `no`, no OTP is sent at all.
  - Detected brute force also triggers an operator alert. See [monitoring](monitoring.md).
- **Forgotten password.** The reply is the same whether or not the username exists. It cannot be used to guess accounts.
- **Customer passwords.** At least `customer_password_min` characters (default 4, allowed 4 to 64). They apply to registration, change, reset, and edits by an admin. A password equal to the username, and common passwords such as `1234` or `password`, are always rejected.
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

1. Log in, click your name at the top right, then **Two-factor login (2FA)**. You can also open `/admin/2fa` directly.
2. Click **Enable 2FA**.
3. Scan the QR code with an authenticator app, or type the key shown below it.
4. Enter the 6-digit code from the app. 2FA becomes active only after this code is correct.
5. Save the 8 **recovery codes** that appear. The codes are shown only once.

### Recovery codes

- They have the form `XXXX-XXXX`. Each code can be used once, and is stored as a bcrypt hash.
- They are used on the second login screen if the authenticator app is not available. Type them with or without the dash.
- After a code is used, it is dead. If all are used, disable and enable 2FA again to get 8 new codes.

### Disabling

On `/admin/2fa`, enter the current password and one 6-digit code from the app, then click **Turn off 2FA**. Remaining recovery codes are deleted too.

### Reset by a SuperAdmin

If an admin loses the app and the recovery codes:

1. A SuperAdmin opens **Admin Users**.
2. The SuperAdmin selects the account, then clicks **Reset 2FA**.

That admin can log in with the password only. 2FA must be enabled again. The action is recorded in the activity log as `users.2fa.reset`. A reset does not ask for the SuperAdmin's password. So protect your own SuperAdmin account with 2FA.

### Limits

- Wrong code attempts count toward the login limit: 10 failures in 15 minutes lock the IP, and the username is slowed.
- A used code cannot be used again. The list of used codes is kept in memory. After a restart, the same code may be accepted again for the rest of its time step, at most about 90 seconds.
- Customer login and the API do not use admin 2FA. Admin sessions are opened only through the login page.

## Forgotten admin password

There are two ways back in. Both set a new random password that is shown once, and all sessions of that admin end, so the admin must log in again.

### From the screen: SuperAdmin

1. A SuperAdmin opens **Admin Users**, selects the account, then clicks **Reset password**. The button does not appear for your own account; use **Change Password** for that.
2. Confirm. Copy the password shown. Once the page is closed it cannot be seen again; if it is lost, reset again.
3. Give the password to the admin. The admin must log in again.

The action is recorded in the activity log as `users.password.reset` with the account name. The password is never logged.

### From the server: CLI command

Use this when no SuperAdmin can log in. Run it on the server with the same `GOBILL_DB` as the service. The service does not need to be stopped.

    GOBILL_DB=/var/lib/gobill/gobill.db gobill reset-admin-password admin

The new password is printed once to the screen. Do not save that output to a log file.

Add `--disable-2fa` if the authenticator app is lost as well. That turns 2FA off and deletes the recovery codes, so the admin can log in with the password alone. In Docker, run `docker exec -it <container-name> gobill reset-admin-password admin` and make sure `GOBILL_DB` points to `/data/gobill.db`.

## See also

- [MikroTik setup](mikrotik.md): RADIUS settings on the router.
- [FreeRADIUS over REST](freeradius-rest.md): the REST path and allow-list.
- [Configuration](configuration.md#network-and-proxy): proxy and allow-list settings.
- [Monitoring](monitoring.md): brute-force alerts and the `/metrics` endpoint.
- [Backup and restore](backup-restore.md): key and database backups.
