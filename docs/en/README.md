# Operator guide (English)

This document is the index of the operator guide in English. The guide is also available in the application, under **Guide**. The files in this folder and in `docs/id/` have the same names. The Indonesian version is at [docs/id/README.md](../id/README.md).

**For:** operators

**Prerequisites:** none

## Getting started

- [Installation](installation.md): install on an Armbian STB, a VPS, or Docker, and troubleshooting.
- [Upgrade](upgrade.md): replace the binary with a new version, version notes, and rollback.
- [Migrating from PHPNuxBill](migration-phpnuxbill.md): import data, cutover, and the parallel-run checklist.

## Configuration

- [Configuration](configuration.md): `GOBILL_*` variables and settings in the UI.
- [Integrations](integrations.md): WhatsApp, SMS, Telegram, email, webhooks, Tripay, and QRIS.
- [Security](security.md): RADIUS hardening, firewall, application security, and admin 2FA.

## Network

- [MikroTik setup](mikrotik.md): API mode and built-in RADIUS mode, and lessons from field testing.
- [FreeRADIUS over REST](freeradius-rest.md): keep using an existing FreeRADIUS.

## Operations

- [Monitoring](monitoring.md): System Status, `/health`, `/metrics`, and operator alerts.
- [Backup and restore](backup-restore.md): daily backups, mirrors, and database recovery.

## Quick search

The search box in the top bar finds customers, subscriptions, invoices, vouchers, plans, routers, NAS, menus, quick actions, and settings. Press `/` or `Ctrl+K` (`Cmd+K` on Mac) to type in the box right away. Up and down arrows choose a result, Enter opens it, and Esc closes it. On a phone, results use the full screen width. Settings go straight to their field, and the field is briefly highlighted. You only see results you are allowed to open for your role.

## See also

- [Project README](../../README.md): project overview. Written in Indonesian.
