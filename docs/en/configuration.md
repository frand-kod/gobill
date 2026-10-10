# Configuration

This document describes the two configuration layers in NuxBill: the `NUXBILL_*` environment variables and the settings in the UI. Environment variables are read before the database opens. With systemd, they are in `/etc/nuxbill/config.env`. Settings are stored in the `settings` table and changed in the **Settings** menu.

**For:** operators

**Prerequisites:** the application is installed. See [installation](installation.md).

## Environment variables

This list contains every variable the code reads in `cmd/nuxbill`.

| Variable | Default | Purpose |
|---|---|---|
| `NUXBILL_DB` | `./nuxbill.db`. In Docker: `/data/nuxbill.db` | Location of the SQLite file. `nuxbill import` also uses it |
| `NUXBILL_HTTP` | `:8080` | HTTP listen address |
| `NUXBILL_HTTPS` | enabled | The session cookie always has the `Secure` flag. Set `0` only if the UI is reached over plain HTTP, for example by a LAN IP. Browsers do not send `Secure` cookies over `http://`, except for `localhost`. When set to `0`, startup logs a warning. |
| `NUXBILL_SECRET_KEY` | empty | AES-GCM key for router, customer, and NAS secrets. If empty, the key is created automatically at `<NUXBILL_DB>.key`. If set, the value replaces the `.key` file and must be stored safely. `nuxbill import` also uses it. |
| `NUXBILL_RADIUS` | `:1812` | RADIUS UDP listen address for authentication. Accounting uses the next port. An empty value or `off` turns off the UDP listener. `/radius.php` keeps running. |
| `NUXBILL_BACKUP_DIR` | a `backup` folder next to the database | Folder for daily backups and for automatic backups before an import, restore, or recovery. See [backup and restore](backup-restore.md). |
| `NUXBILL_BACKUP_MIRROR` | empty, off | A second copy of the daily backups in another folder, for example USB, NAS, or rclone. The folder must already exist. See [backup and restore](backup-restore.md#mirror). |
| `NUXBILL_TEST_MYSQL_DSN`, `NUXBILL_TEST_PHP_SQL` | empty | Only for importer tests. They are not read while the application runs. See the [developer guide](../internal/pengembangan.md), which is in Indonesian only. |

Example `/etc/nuxbill/config.env`:

    NUXBILL_DB=/var/lib/nuxbill/nuxbill.db
    NUXBILL_HTTP=:8080
    NUXBILL_RADIUS=:1812
    NUXBILL_SECRET_KEY=<SECRET>

CLI options:

- `nuxbill --version` shows the version.
- `nuxbill import ...` moves data from PHPNuxBill. See [migration](migration-phpnuxbill.md).

## First admin

When the database is empty and has no admin, NuxBill creates the user `admin` with the SuperAdmin role. Its password is random and 16 characters long. The password is not printed to the log. It is written to `initial-admin-password.txt` in the database folder, with mode 0600.

Log in, change the password, then delete that file. If the file already exists and the database is still empty, startup stops with a message. Delete the old file first.

## Settings in the UI

Change settings in the **Settings** menu. The keys below are stored in the `settings` table. If a default is not listed, the value is empty or `no`.

### General and localisation

| Setting | Purpose |
|---|---|
| `company_name` | Business name. Shown on invoices, messages, and the login page. |
| `app_url` | Public address of the application, for example `https://billing.example.com`. It is filled in automatically from the address the first admin uses to log in, unless that address is a LAN address or localhost. It is used for links in WhatsApp messages, such as QRIS and invoices. |
| `timezone` | Display time zone, for example `Asia/Jakarta`. Times are stored in UTC. An empty value means UTC, so fill it in during the first setup. |
| `country_code_phone` | Country code for numbers that start with `0`, for example `62`. |
| `default_plan_device` | Default device for new plans: `MikrotikHotspot`, `MikrotikPppoe`, `Dummy`, `Radius`, or empty. Empty means the device follows the plan type. |
| `maintenance_mode`, `maintenance_mode_logout`, `maintenance_date` | Maintenance mode. The customer portal is closed. `maintenance_mode_logout` also logs out customers who are logged in. |
| `reminder_hour` | Hour when daily reminders are sent, from 0 to 23. Default 7. |
| `clock_guard` | `off` turns off the clock guard. Use it only with an accurate RTC. Default `on`. See [installation](installation.md#3-clock-and-ntp). |
| `log_keep_days` | How long to keep logs, closed RADIUS sessions, read inbox messages, and unpaid payments, in days. Default 90 if never set. `0` means keep forever. Paid payments are never deleted. |
| `backup_keep` | Number of daily backup files to keep, in the main folder and in the mirror. Default 7. |

### Admin and sessions

| Setting | Purpose |
|---|---|
| `session_timeout_duration` | Idle timeout for admin and portal sessions, in minutes. Default 120. |
| `single_session` | `yes` allows only one active session per admin. |

Admin roles and 2FA are set per account in **Admin Users** and on the `/admin/2fa` page, not in **Settings**. See [security](security.md#two-step-verification-2fa-for-admins).

### Network and proxy

| Setting | Purpose |
|---|---|
| `radius_rest_allow` | Comma-separated list of IPs or CIDRs that may call `/radius.php`. Empty means loopback only: `127.0.0.0/8` and `::1`. Fill it with the FreeRADIUS IP if FreeRADIUS runs on another host. At startup, NuxBill logs a warning if it is empty. |
| `trust_proxy` | `yes` trusts `X-Forwarded-For`. The header is trusted only if the direct connection comes from loopback or from an IP or CIDR in `trusted_proxies`. Default `no`. NuxBill uses the rightmost entry as the client IP, so the proxy must write the real IP there. |
| `trusted_proxies` | Comma-separated IPs or CIDRs of reverse proxies, for example `172.16.0.0/16, 10.0.0.2`. Loopback is always trusted. The `/radius.php` allow-list still uses the real connection address, not the `X-Forwarded-For` value. |
| `router_check` | `no` turns off the router check. It is on by default. A down router triggers an alert. See [monitoring](monitoring.md). |

Example Nginx in front of the application on the same host:

- Set `trust_proxy` = `yes`.
- In Nginx, write `proxy_set_header X-Forwarded-For $remote_addr;`.

If Cloudflare is in front of Nginx, set `set_real_ip_from` in Nginx to the Cloudflare IP ranges, and `real_ip_header CF-Connecting-IP`.

### Business

| Setting | Purpose |
|---|---|
| `extend_expiry` | Extend an active plan by adding time from its expiry date. Enabled by default if never set. |
| `admin_extend` | Who may use the Extend button in the subscription list. `staff` is the default and means Admin, Agent, and Sales. `managers` means SuperAdmin and Admin. `super` means SuperAdmin only. `off` hides the button. |
| `enable_balance` | Balance system: top up balance, transfer balance, and auto-renew from balance. Enabled by default if never set. |
| `start_on_first_login` | `yes` starts the validity of a RADIUS plan at the first login, not at recharge. Default `no`. See the "Start on first login" section below. |
| `disable_registration`, `disable_voucher`, `allow_phone_otp`, `registration_username`, `phone_otp_type` | Restrictions and methods in the customer portal. `phone_otp_type` selects `sms` or `wa`. |
| `voucher_format` | Default voucher code format: `up`, `low`, `rand`, or `numbers`. |
| `hs_auth_method` | Hotspot auth method shown under **Miscellaneous**: `pap` or `chap`. This setting does not change the built-in RADIUS server. The built-in server accepts PAP, CHAP, and MS-CHAPv2. |
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

`logo`, `logo_dark`, `login_page_*`, `date_format`, `dec_point`, `thousands_sep`, and `language` are set in **Settings**. The light or dark theme and the accent colour are set from the header, not from these settings.

## See also

- [Installation](installation.md): setup and first variables.
- [Integrations](integrations.md): WhatsApp, Telegram, email, webhook, Tripay, and QRIS.
- [Monitoring](monitoring.md): operator alerts, daily summary, and `/metrics`.
- [Security](security.md): secrets, proxies, and admin 2FA.
- [Backup and restore](backup-restore.md): backup, mirror, and recovery.
