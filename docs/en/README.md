# Operator guide (English)

This document is the index of the operator guide in English. The guide is also available in the application, under **Guide**. The files in this folder and in `docs/id/` have the same names. The Indonesian version is at [docs/id/README.md](../id/README.md).

**For:** operators

**Prerequisites:** none

## Getting started

- [Installation](installation.md): install on an Armbian STB, a VPS, or Docker, and troubleshooting.
- [Quick setup](quick-setup.md): step-by-step guide after installation, or import from PHPNuxBill.
- [Upgrade](upgrade.md): replace the binary with a new version, version notes, and rollback.
- [Migrating from PHPNuxBill](migration-phpnuxbill.md): import data, cutover, and the parallel-run checklist.

## Configuration

- [Configuration](configuration.md): `GOBILL_*` variables and settings in the UI.
- [Integrations](integrations.md): WhatsApp, SMS, Telegram, email, webhooks, Tripay, and QRIS.
- [Security](security.md): RADIUS hardening, firewall, application security, and admin 2FA.
- [Promotions](promotions.md): short promo cards on the isolir page and the portal dashboard.

## Network

- [MikroTik setup](mikrotik.md): API mode and built-in RADIUS mode, access points behind the router, and lessons from field testing.
- [FreeRADIUS over REST](freeradius-rest.md): keep using an existing FreeRADIUS.

## Operations

- [Monitoring](monitoring.md): System Status, `/health`, `/metrics`, and operator alerts.
- [Backup and restore](backup-restore.md): daily backups, mirrors, and database recovery.

## Customers

On the customer list, tick several customers and choose **Recharge same plan**. Each customer is recharged on the plan they are on now, paid in cash, with the same rules as a single recharge. The confirmation shows the number of customers and the total. The result lists how many succeeded and the reason for each failure. The limit is 200 customers per action. Do not reload the result page, because the recharge runs again.

The customer list has an **Ends On** column: the end date of the nearest active plan. The column can be sorted. The label **Today** means the plan ends today.

The diagnosis strip on the customer page gives one next step for each failing check: account, plan (ended, isolated, or not started until the first login), connection (online with NAS, IP and how long it has been connected, or offline with the end of the last session), quota, and router.

## Boarding house pattern

This pattern uses features that already exist. No new feature needs to be turned on.

Example: one boarding house with many rooms. Each room has one account.

1. Create one customer account per room. Use a fixed username, for example `kamar05`. The username does not change when the tenant changes.
2. Reset the password each time a tenant moves out, so the old tenant cannot log in.
3. Set up a plan with the speed limit you want. The limit applies to the account of that room.
4. Set `shared_users` on the plan for tenants who have more than one device.
5. Use a postpaid plan for rent. Activate the plan from the customer page when the rent is paid. Until then, the plan is not active.

If the router in the boarding house sits behind an AP, make sure the AP is in bridge mode. See [access points behind the router](mikrotik.md#access-points-behind-the-router).

## Quick search

The search box in the top bar finds customers, subscriptions, invoices, vouchers, plans, routers, NAS, menus, quick actions, and settings. Press `/` or `Ctrl+K` (`Cmd+K` on Mac) to type in the box right away. Up and down arrows choose a result, Enter opens it, and Esc closes it. On a phone, results use the full screen width. Settings go straight to their field, and the field is briefly highlighted. You only see results you are allowed to open for your role.

The add customer form has an optional **Langsung aktifkan paket** card. Pick a plan there to activate it right after the customer is created, paid in Cash. Only plans that fit the service type are listed, and Balance plans are left out. If activation fails, the customer is still created, and the customer page shows the reason. Leave the plan empty to choose one later from the customer page.

## Not supported yet

- Plan change via CoA for Radius plans: in progress. Until then, speed changes apply when the customer reconnects. CoA for disconnecting sessions already works.
- Native RadSec: use radsecproxy or WireGuard. See [security](security.md).
- WireGuard on RouterOS 6: that version does not have it. Use L2TP/IPsec or upgrade RouterOS.
- PHPNuxBill import: coupons, ODP, inbox, and the `email_invoice` template are not imported. The `frrest_interim_update` setting is not read. See [migration](migration-phpnuxbill.md).

## See also

- [Project README](../../README.md): project overview. Written in Indonesian.
