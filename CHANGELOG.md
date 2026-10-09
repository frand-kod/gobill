# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project uses
[Semantic Versioning](https://semver.org/).

Versions 0.x mean pre-1.0: breaking changes may happen in minor versions.

## [Unreleased]

### Added

- License GPL-3.0-or-later, NOTICE, CONTRIBUTING, issue/PR templates.

### Changed

- Go module path is now `github.com/frand-kod/gobill` (was `github.com/frand-kod/nuxbill-go`).

## [0.1.0] - 2026-10-09

First release: a single-binary rewrite of PHPNuxBill (MikroTik hotspot and PPPoE billing).

### Added

- Billing core: plans, customers, recharge, deposit and subscriptions, with balances stored as integer rupiah and atomic balance changes.
- MikroTik driver with RouterOS API mode. *Field-tested* (RouterOS 6.49.22): connection, profile sync, recharge, queues, expiry, and plan changes.
- Built-in RADIUS server (auth and accounting), plus FreeRADIUS-compatible REST endpoint `/radius.php`, hotspot voucher login, and CoA Disconnect. *Field-tested:* built-in RADIUS auth and accounting. *Untested:* CoA re-test after the NAS-IP fix, and FreeRADIUS REST.
- Customer portal: balance orders, voucher activation, extend, balance transfer, OTP, and inbox.
- Tripay payment gateway flow. *Untested:* Tripay sandbox.
- Admin UI: dashboard widgets, reports and invoices, coupons, maps and ODP, custom fields, static pages, admin users, router monitor, and admin logs.
- Notifications and a reminder job, with webhook in place of the old plugin system.
- Import from a PHPNuxBill MySQL database, run in a single transaction with a dry-run report.
- Daily database backup, maintenance mode, and clock guard for hardware without an RTC. *Untested:* STB hardware.
- Release CI for linux amd64, arm64, and armv7, with `sha256sums.txt`.

### Security

- bcrypt for admin and customer passwords. Legacy SHA-1 hashes are rehashed on first login.
- AES-GCM encryption for router, customer, and NAS secrets.
- CSRF protection, brute-force throttles for login, voucher, and OTP, and RADIUS per-user auth throttles.
- Sessions are revoked when a password, role, or status changes.
- RADIUS hardening: strong NAS secrets, duplicate request detection, and Message-Authenticator on responses (BlastRADIUS).
- Notification errors are redacted before logging, so tokens and API keys do not leak.

### Fixed

- Hotspot and PPPoE users created with an empty password.
- Empty timezone setting fell back to UTC.
- The REST path reset brute-force throttles; now one shared RADIUS server holds them.
- `NUXBILL_RADIUS=` (empty) did not disable the UDP listener.
- CoA sent the wrong NAS-IP-Address. Also decodes Error-Cause and logs unknown NAS.
- Requests from unregistered NAS were dropped without a log entry.
