# Monitoring

This document describes how to monitor NuxBill. There are three ways: the System Status page in admin, the `/health` endpoint for uptime monitors, and the `/metrics` endpoint for Prometheus. NuxBill also sends alerts to the operator automatically.

**For:** operators

**Prerequisites:** NuxBill is running. For alerts, the Telegram or WhatsApp channel is set up in [integrations](integrations.md).

## System Status page

Open **Admin > System Status** (`/admin/status`). SuperAdmin and Admin can open this page. The page refreshes itself every 30 seconds.

| Section | Content |
|---|---|
| Application | Version, start time, uptime, memory in use, and number of goroutines. |
| Storage | Database and WAL size, free disk space, daily database growth, and the estimated number of days until the disk is full. The estimate appears only after two daily samples. Samples are stored once a day, and the last 30 days are kept. |
| RADIUS | Packets accepted and rejected in the last 24 hours, average processing time, open sessions, and the last packet per NAS. A NAS that is silent for longer than the alert limit is marked red. |
| Background jobs | Last run time, duration, last error, and the number of consecutive failures. |
| Notifications | Sent and failed counts per channel, namely WA, SMS, email, Telegram, and webhook, with the last error. Errors have URLs, tokens, and phone numbers removed. |
| Payments | Successful and failed Tripay callbacks, and the time of the last callback. |
| Security | Failed logins in the last 24 hours and the last 10 minutes, failed 2FA, active login locks, and rejected `/radius.php` requests. |
| Backup | Last local backup, marked if it is older than 36 hours, and the status of the mirror copy. |

The same data is available as JSON at `/admin/status.json`, with the same admin session.

Counts "since start" exist only in memory. A restart clears them. The 24-hour counts fill up again over time.

## /health and /metrics

| | `/health` | `/metrics` |
|---|---|---|
| For | Uptime monitors, for example Uptime Kuma or UptimeRobot | Prometheus, Grafana, or other scrapers |
| Format | JSON | Prometheus text |
| Authentication | Not required | Bearer token |
| Enabled | Always | Only if a token is set |
| Content | `status` and `db` | All counters and gauges, including free disk space and version |

### /health

Example response:

    curl -s https://domain-anda/health
    {"status":"ok","db":"ok"}

Values of `status`:

- `ok`: normal.
- `degraded`: free disk space in the database folder is below 200 MB. The response is still HTTP 200.
- `down`: the database cannot be read. The response is HTTP 503.

This endpoint does not need a login. It does not show customer data, and it stays reachable during maintenance mode.

### Enabling /metrics

1. Log in as SuperAdmin.
2. Open **Settings > Integrations**, section **Prometheus /metrics**.
3. Click **Create new token**. A 64-character hex token is shown once, at the top of the page, together with an example scrape configuration. Copy it now. The token is not shown again on later pages. The token field only shows "saved".
4. To turn it off, click **Disable**. After that, `/metrics` returns `404`.

A request without the correct header gets `401`. The token is compared with a constant-time comparison. The token value is stored in the `metrics_token` setting. If it is empty, `/metrics` is off.

### Prometheus scrape example

    scrape_configs:
      - job_name: gobill
        metrics_path: /metrics
        scrape_interval: 60s
        authorization:
          type: Bearer
          credentials: TOKEN_FROM_SETTINGS
        static_configs:
          - targets: ['192.168.1.10:8080']

Test with curl:

    curl -H 'Authorization: Bearer TOKEN_FROM_SETTINGS' http://192.168.1.10:8080/metrics

Metric names use the prefix `gobill_`, for example `gobill_radius_auth_accepted_total`, `gobill_notifications_failed_total{channel="wa"}`, and `gobill_db_size_bytes`.

### Uptime Kuma tip

Use a monitor of type **HTTP(s) - Keyword** for `http://IP:8080/health`, with the keyword `"status":"ok"`. This way, a `degraded` status is also treated as not OK, and you get notified sooner. To monitor RADIUS, use a TCP or UDP monitor on port 1812 of the server.

### Monitoring from outside

NuxBill cannot report that it is down by itself. Set up an external monitor, for example UptimeRobot or Uptime Kuma. This monitor calls `https://domain-anda/health` every 5 minutes, and sends an alarm if the response is not 200.

## Operator alerts

NuxBill checks the following rules every minute. Each rule sends one alert when a problem starts, and one alert when it recovers. While the problem continues, no more messages are sent. The state is kept in memory. After a restart, a problem that is still active is reported again.

| Rule | Triggered when | Recovered when |
|---|---|---|
| Disk almost full | Free disk space in the database folder is below 200 MB | Free disk space is 200 MB or more |
| NAS silent | A NAS in the NAS menu has sent RADIUS packets, then is silent for `alert_nas_silent_minutes` minutes. Default 15 | A packet arrives again |
| Job failed | A job fails 3 times in a row | One run succeeds |
| Notification channel failed | A channel fails 5 times in a row | One send succeeds |
| Brute force | More than 30 login failures, admin, portal, and 2FA, in 10 minutes | Failures drop below the limit |
| Backup | The last local backup is more than 36 hours old, or a mirror copy failed | A new backup exists, or a mirror copy succeeds |
| Abnormal restart | NuxBill stopped because of a crash, a forced kill, or a power cut | - |

A failed mirror backup is retried once an hour, for the rest of that day, until it succeeds.

An alert is also sent if a paid payment callback arrives for a customer who has been deleted.

### Alert settings

These settings are in **Settings > Notifications**, section **Operator alerts**.

| Setting | Purpose |
|---|---|
| `alert_channel` | `telegram` (default), `wa`, or `both`. Telegram uses `telegram_target_id`. WhatsApp uses the GOWA server or `wa_url`. |
| `alert_wa_to` | WhatsApp number for alerts. If empty, `daily_summary_wa_to` is used. |
| `alert_nas_silent_minutes` | A NAS that is silent for N minutes triggers an alert. Default 15, range 1 to 1440. |

Alerts about routers going offline and devices restarting also use this channel.

## Daily summary

The daily summary is sent to the operator, not to customers. Its settings are in **Settings > Notifications**.

| Setting | Purpose |
|---|---|
| `daily_summary_enabled` | `yes` or `no`. Default `no`. |
| `daily_summary_time` | Time in `HH:MM` format, in the server time zone. Default `07:00`. |
| `daily_summary_channel` | `telegram`, `wa`, or `both`. |
| `daily_summary_wa_to` | Operator's WhatsApp number. |

The summary is sent once a day. The last date is stored in `daily_summary_last`, so restarts are safe. The summary is postponed if the system clock is not trusted. The button "Send summary now" in **Settings > Notifications** is used for test runs.

## See also

- [Integrations](integrations.md): Telegram and WhatsApp channels, and connection tests.
- [Configuration](configuration.md): general settings and the clock guard.
- [Backup and restore](backup-restore.md): backups and mirrors that alerts watch.
- [Security](security.md): public endpoints and login limits.
