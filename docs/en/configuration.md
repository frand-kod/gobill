# Configuration

This document describes the two configuration layers in gobill: the `GOBILL_*` environment variables and the settings in the UI. Environment variables are read before the database opens. With systemd, they are in `/etc/gobill/config.env`. Settings are stored in the `settings` table and changed in the **Settings** menu.

**For:** operators

**Prerequisites:** the application is installed. See [installation](installation.md).

## Environment variables

This list contains every variable the code reads in `cmd/gobill`.

| Variable | Default | Purpose |
|---|---|---|
| `GOBILL_DB` | `./gobill.db`. In Docker: `/data/gobill.db` | Location of the SQLite file. `gobill import` also uses it |
| `GOBILL_HTTP` | `:8080` | HTTP listen address. For isolir, set it to the LAN IP and port, for example `192.168.99.2:8090`, so the MikroTik commands are correct. If empty or `0.0.0.0`, gobill uses the first LAN IPv4 address of the host. |
| `GOBILL_HTTPS` | enabled | The session cookie always has the `Secure` flag. Set `0` only if the UI is reached over plain HTTP, for example by a LAN IP. Browsers do not send `Secure` cookies over `http://`, except for `localhost`. When set to `0`, startup logs a warning. |
| `GOBILL_PPROF` | off | Address of the Go profiler (`net/http/pprof`), for example `127.0.0.1:6060`. For benchmarking only. It has no login, so never bind it to a public address. |
| `GOMEMLIMIT` | automatic | Go memory soft limit. If unset, gobill sets it to 70% of the cgroup (container or systemd `MemoryMax`) limit, or 50% of system RAM, and logs `memory budget` at startup. With a budget of 256 MB or less the SQLite cache shrinks from 8 MB to 2 MB per connection. Tested to run in 64 MB with 5000 customers on one core. |
| `GOMAXPROCS` | automatic, at least 2 | Go threads that run at once. On a one-core device gobill uses 2, so a thread waiting on the disk (SQLite `fsync`) does not stop the RADIUS server. Set it only to override this. |
| `GOBILL_SECRET_KEY` | empty | AES-GCM key for router, customer, and NAS secrets. If empty, the key is created automatically at `<GOBILL_DB>.key`. If set, the value replaces the `.key` file and must be stored safely. `gobill import` also uses it. |
| `GOBILL_RADIUS` | `:1812` | RADIUS UDP listen address for authentication. Accounting uses the next port. An empty value or `off` turns off the UDP listener. `/radius.php` keeps running. |
| `GOBILL_BACKUP_DIR` | a `backup` folder next to the database | Folder for daily backups and for automatic backups before an import, restore, or recovery. See [backup and restore](backup-restore.md). |
| `GOBILL_BACKUP_MIRROR` | empty, off | A second copy of the daily backups in another folder, for example USB, NAS, or rclone. The folder must already exist. See [backup and restore](backup-restore.md#mirror). |
| `GOBILL_STALE_AFTER` | `10m` | Advanced. How long an open RADIUS session may go without an Interim-Update before it is stale. Stale sessions do not count toward `shared_users`. Set it to about 2x the NAS interim interval, for example `20m` for a 10-minute interim. Accepts `1m` to `1h`. A bad value stops startup. |
| `GOBILL_NAS_ONLINE_WINDOW` | `15m` | Advanced. A NAS shows as online on the dashboard while its newest RADIUS packet is this recent. Accepts `1m` to `24h`. A bad value stops startup. |
| `GOBILL_TEST_MYSQL_DSN`, `GOBILL_TEST_PHP_SQL` | empty | Only for importer tests. They are not read while the application runs. See the [developer guide](../internal/pengembangan.md), which is in Indonesian only. |

`install.sh` writes a fully commented `/etc/gobill/config.env` on a new install. Every variable above is explained there. To see the template for your version, run `gobill config-example`. The short form is:

    GOBILL_DB=/var/lib/gobill/gobill.db
    GOBILL_HTTP=:8080
    GOBILL_RADIUS=:1812
    GOBILL_SECRET_KEY=<SECRET>

CLI options:

- `gobill --version` shows the version.
- `gobill config-example` prints the commented `config.env` template.
- `gobill import ...` moves data from PHPNuxBill. See [migration](migration-phpnuxbill.md).
- `gobill update` and `gobill rollback` replace the binary. See [upgrade](upgrade.md).
- `gobill reset-admin-password <username>` sets a new password for an admin account. See [security](security.md#forgotten-admin-password).

## First admin

When the database is empty and has no admin, gobill creates the user `admin` with the SuperAdmin role. Its password is random and 16 characters long. The password is not printed to the log. It is written to `initial-admin-password.txt` in the database folder, with mode 0600.

Log in at `/admin/login`, change the password, then delete that file. If the file already exists and the database is still empty, startup stops with a message. Delete the old file first.

## Settings in the UI

Change settings in the **Settings** menu. The keys below are stored in the `settings` table. If a default is not listed, the value is empty or `no`. `log_keep_days` and `backup_keep` are set in **Settings > System & Security**. `radius_rest_allow` is there too, but only a SuperAdmin sees and changes it.

### General and localisation

| Setting | Purpose |
|---|---|
| `company_name` | Business name. Shown on invoices, messages, and the login page. |
| `app_url` | Public address of the application, for example `https://billing.example.com`. It is filled in automatically from the address the first admin uses to log in, unless that address is a LAN address or localhost. It is used for links in WhatsApp messages, such as QRIS and invoices. The customer login is at `/login` (the welcome message links there), and the admin login is at `/admin/login`. |
| `timezone` | Display time zone, for example `Asia/Jakarta`. Times are stored in UTC. An empty value means UTC, so fill it in during the first setup. |
| `country_code_phone` | Country code for numbers that start with `0`, for example `62`. |
| `default_plan_device` | Default device for new plans: `MikrotikHotspot`, `MikrotikPppoe`, `Dummy`, `Radius`, or empty. Empty means the device follows the plan type. |
| `maintenance_mode`, `maintenance_mode_logout`, `maintenance_date` | Maintenance mode. The customer portal is closed. `maintenance_mode_logout` also logs out customers who are logged in. |
| `reminder_hour` | Hour when daily reminders are sent, from 0 to 23. Default 7. |
| `clock_guard` | `off` turns off the clock guard. Use it only with an accurate RTC. Default `on`. See [installation](installation.md#3-clock-and-ntp). |
| `log_keep_days` | How long to keep logs, closed RADIUS sessions, read inbox messages, and unpaid payments, in days. Default 90 if never set. `0` means keep forever. Paid payments are never deleted. |
| `backup_keep` | Number of daily backup files to keep, in the main folder and in the mirror. Default 7. |

### System and security

| Setting | Purpose |
|---|---|
| `session_timeout_duration` | Idle timeout for admin and portal sessions, in minutes. Default 120. |
| `single_session` | `yes` allows only one active session per admin. |
| `update_check` | `no` turns off the check for new releases on GitHub. Default `yes`. When a newer release exists, SuperAdmin sees a `vX.Y.Z available` badge in the header. |

Admin roles and 2FA are set per account in **Admin Users** and on the `/admin/2fa` page, not in **Settings**. See [security](security.md#two-step-verification-2fa-for-admins).

### Network and proxy

| Setting | Purpose |
|---|---|
| `radius_rest_allow` | Comma-separated list of IPs or CIDRs that may call `/radius.php`. Empty means loopback only: `127.0.0.0/8` and `::1`. Fill it with the FreeRADIUS IP if FreeRADIUS runs on another host. At startup, gobill logs a warning if it is empty. |
| `trust_proxy` | `yes` trusts `X-Forwarded-For`. The header is trusted only if the direct connection comes from loopback or from an IP or CIDR in `trusted_proxies`. Default `no`. gobill uses the rightmost entry as the client IP, so the proxy must write the real IP there. |
| `trusted_proxies` | Comma-separated IPs or CIDRs of reverse proxies, for example `172.16.0.0/16, 10.0.0.2`. Loopback is always trusted. The `/radius.php` allow-list still uses the real connection address, not the `X-Forwarded-For` value. |
| `router_check` | `no` turns off the router check. It is on by default. A down router triggers an alert. See [monitoring](monitoring.md). |

Example Nginx in front of the application on the same host:

- Set `trust_proxy` = `yes`.
- In Nginx, write `proxy_set_header X-Forwarded-For $remote_addr;`.

If Cloudflare is in front of Nginx, set `set_real_ip_from` in Nginx to the Cloudflare IP ranges, and `real_ip_header CF-Connecting-IP`.

### Customers and billing

| Setting | Purpose |
|---|---|
| `extend_expiry` | Extend an active plan by adding time from its expiry date. Enabled by default if never set. |
| `admin_extend` | Who may use the Extend button in the subscription list. `staff` is the default and means Admin, Agent, and Sales. `managers` means SuperAdmin and Admin. `super` means SuperAdmin only. `off` hides the button. |
| `enable_balance` | Balance system: top up balance, transfer balance, and auto-renew from balance. Enabled by default if never set. |
| `start_on_first_login` | `yes` starts the validity of a RADIUS plan at the first login, not at recharge. Default `no`. See the "Start on first login" section below. |
| `disable_registration`, `disable_voucher`, `allow_phone_otp`, `phone_otp_type`, `customer_password_min` | Restrictions and methods in the customer portal. `phone_otp_type` selects `sms` or `wa`. `customer_password_min` is the minimum customer password length, 4 to 64. |
| `wa_unique_per_account` | `yes` allows a WhatsApp number to belong to one account only: registration, the customer's contact change and the admin's customer form refuse a number another account uses. `no` (default) lets several accounts share one number, for example a family or an RT/RW group. Forgot username then sends every username on that number. Forgot password asks for the username, so the code goes to that account's number. |
| `voucher_format` | Default voucher code format: `up`, `low`, `rand`, or `numbers`. |
| `hs_auth_method` | Hotspot auth method shown under **System & Security**: `pap` or `chap`. This setting does not change the built-in RADIUS server. The built-in server accepts PAP, CHAP, and MS-CHAPv2. |
| `shared_users_policy` | What a Hotspot plan does when its device limit (`shared_users`) is full and a new device logs in. `kick_oldest` (default, also when never set) drops the customer's oldest online session on its NAS and accepts the new login. `reject` refuses the new login with "Akun sedang dipakai perangkat lain..." ("the account is in use by another device"). Phones with random MACs reconnect as a new device, so `kick_oldest` avoids locking out a single-device customer. |
| `check_customer_online` | `yes` shows on the customer page whether the customer is connected through a plan device. |

### Start on first login (`start_on_first_login`)

If `yes`, a new recharge of a RADIUS plan, device `Radius`, or a recharge after the plan has run out does not start the validity period right away. The subscription is marked as waiting. The customer's first RADIUS login after that starts the validity period from the login time, with the full plan duration.

While waiting:

- the customer can still log in;
- the expiry job does not treat the subscription as expired;
- the expiry date shows as "Starts at first login" in admin, the portal, and WhatsApp recharge messages.

Other rules:

- Only RADIUS plans are affected. MikroTik plans, hotspot or PPPoE with a router, always start at recharge.
- Recharging a plan that has not been used yet waits again from the beginning.
- Extending a plan that is running still follows `extend_expiry`.

### Message and OTP switches

The switches `notify_customers`, `notify_otp`, and `expired_notify_minutes_before` are described in [integrations](integrations.md#message-switches). Operator messages are not affected by these switches.

### Appearance

The logo is on the **General** tab. The login page keys (`login_page_*`) are on the **Appearance** tab. Date format, number format, and `language` are on the **Localisation** tab.

## Service plans and audience

Each plan (**Service Plan** menu) has an audience, set in the plan form under **Who can use this plan**:

- **Public** (default): sold on the customer portal. A customer can buy it from balance or pay it through the gateway, send it to a friend, extend it after it expires, and it is renewed from balance when auto-renewal is on.
- **Admin only**: for internal plans such as staff or technicians. It is hidden from the customer portal. A customer cannot buy, pay, send or extend it. An admin can still give it: recharge, bulk recharge, add a customer with a plan, and vouchers. Once given, it renews itself when the customer has auto-renew on: free for a Rp 0 plan, otherwise paid from the balance.

A plan with price Rp 0 is never sold on the portal, whatever its audience. A coupon that brings a public plan to Rp 0 still works. An admin-only plan or a Rp 0 plan is not renewed from balance when it expires: it expires and is isolated like any other plan. Existing plans are public after the upgrade (migration 0017).

## See also

- [Installation](installation.md): setup and first variables.
- [Integrations](integrations.md): WhatsApp, Telegram, email, webhook, Tripay, and QRIS.
- [Monitoring](monitoring.md): operator alerts, daily summary, and `/metrics`.
- [Security](security.md): secrets, proxies, and admin 2FA.
- [Backup and restore](backup-restore.md): backup, mirror, and recovery.
