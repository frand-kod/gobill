# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project uses
[Semantic Versioning](https://semver.org/).

Versions 0.x mean pre-1.0: breaking changes may happen in minor versions.

## [Unreleased]

## [0.1.2] - 2026-10-10

Still pre-1.0: behaviour parity with PHPNuxBill, UI and operator workflow. Not yet field-tested against the WhatsApp server or in a parallel run.

### Added

- WhatsApp sent straight to the "Alternative WhatsApp Gateway" server (`alt_wga_*` settings), with a test button; a `wa_url` pointing at the old PHP plugin is ignored.
- Daily summary for the operator via Telegram and/or WhatsApp.
- `nuxbill import --notifications` imports the PHP notification templates; customer attributes (`Bill`, `Invoice`, `Expired Date`) are imported and honoured.
- Recharge page with a customer picker and a customer summary panel that warns about an active package (double charge guard); the same picker on Add balance and Redeem voucher.
- Bulk delete for vouchers and coupons, prune of used vouchers older than 3 months, message to selected customers, bulk disconnect of online sessions.
- Live search, filters, sorting and reset on lists; global customer search in the header.
- Customer "Diagnosa" card, dashboard "Network" card with router/NAS health and a live router check, collapsible dashboard cards, rolling 12-month charts.
- In-app guide (Panduan) rendered from the operator docs.
- Theme control in the header: light/dark/system, accent colour, density, text size; dark logo variants; PWA manifests.
- Short explanation on every page, toast notifications, footer with version.

### Changed

- Business behaviour aligned with PHPNuxBill after an audit (docs/BUSINESS-PARITY.md): extend restarts expired subscriptions, recharge refused for non-Active customers, one price formula (plan or Invoice, coupon, tax, bills), payment methods from `payment_usings` plus Recharge Zero, dashboard income excludes balance-paid rows and uses `reset_day`, data usage resets on reactivation, cumulative time limit in the built-in RADIUS.
- Deleting a customer keeps the transactions and removes active plans from the router.
- Username can be edited and is synced to the router.
- New design system (tokens, typography, spacing), light sidebar, phone layout without horizontal overflow.
- `internal/web` and `internal/billing` files grouped by feature (move only).

### Fixed

- Router failure during recharge is now shown to the operator instead of "Recharge Successful".
- Notification placeholders like `[[price]]` are filled; unknown ones are removed.
- Settings show the stored value of every field, including image previews.

## [0.1.1] - 2026-10-09

### Added

- License GPL-3.0-or-later, NOTICE, CONTRIBUTING, issue/PR templates.

### Changed

- Go module path is now `github.com/frand-kod/gobill` (was `github.com/frand-kod/nuxbill-go`).

### Fixed

- CI: committed `web/static/app.css` was stale, so the CSS freshness check failed.

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
