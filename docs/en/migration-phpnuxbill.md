# Migrating from PHPNuxBill

This document is for operators who move from PHPNuxBill with MySQL to gobill. The import is done once, either in the UI or with the `gobill import` command. The old system is not changed.

**For:** operators

**Prerequisites:** a JSON backup from PHPNuxBill, or MySQL access to the old database. gobill is installed. See [installation](installation.md).

## In the UI

This method does not need a terminal. A SuperAdmin opens **Settings > Miscellaneous > Import PHPNuxBill**, at `/admin/settings/miscellaneous/import`.

1. Upload the JSON backup file, which is required, and `system/uploads/notifications.json`, which is optional. Click **Check**. No data changes. The report shows, per table: the number of rows read, the rows to import, and the rows skipped with the reason. It also shows the current number of gobill records.
2. Click **Import now**. Tick "I understand the data will be overwritten".
3. Before the import, gobill creates an automatic database backup. See [import safety](backup-restore.md#import-safety). If the backup fails, the import does not run.
4. Old data is overwritten, including admins. You are then sent to the login page. Log in with the old PHPNuxBill admin account and the same password. Old sha1 hashes are changed to bcrypt at the first login.
5. After the import finishes, delete the JSON backup file from your computer, because it contains old passwords.

The uploaded file can be up to 200 MB. An upload that is not confirmed is deleted automatically after 30 minutes, or when it is cancelled.

## Import command

The recommended method is a JSON backup from PHPNuxBill, with no MySQL access.

1. In PHPNuxBill, open **Settings > Database Status**. Tick all tables. At least these tables must be included: `tbl_customers`, `tbl_plans`, `tbl_bandwidth`, `tbl_routers`, `tbl_pool`, `tbl_user_recharges`, `tbl_voucher`, `tbl_users`, and `tbl_appconfig`. Click **Backup**.
2. Run:

        gobill import --json=/path/phpnuxbill_backup.json --db=./gobill.db [--timezone=Asia/Jakarta] [--dry-run] [--force] [--notifications=/path/phpnuxbill/system/uploads/notifications.json]

The backup file contains customer passwords and router secrets in the old format. Keep the file only for yourself. Delete it after the import.

The other option is direct MySQL. Choose one: `--json` or `--mysql-dsn`.

    gobill import --mysql-dsn='<user>:<password>@tcp(127.0.0.1:3306)/phpnuxbill' --db=./gobill.db [--timezone=Asia/Jakarta] [--dry-run] [--force] [--notifications=/path/phpnuxbill/system/uploads/notifications.json]

Notes:

- Tables that are not in the backup file, or not in the database, are skipped. Each skipped table is noted in the report as "not present or empty in source, skipped". The import does not fail for this reason.
- Everything runs in one SQLite transaction. The target must be empty. The `--force` option deletes its contents.
- `--dry-run` only creates the report. The report contains the rows read, imported, and skipped, with the reasons.
- The default time zone comes from the old settings, or `Asia/Jakarta`.
- Message templates, namely expiry, reminders 7, 3, and 1 days before, invoice, welcome, and balance, are stored by PHPNuxBill in `system/uploads/notifications.json`, not in MySQL. Add `--notifications=<path to that file>` so the templates are imported into the `notif_*` settings. Without this option, or if the file does not exist, messages use the default English templates, and the import report notes this.
- Only templates that have an equivalent in gobill are imported. `email_invoice` is skipped.
- Unknown placeholders are sent as empty text, never as raw `[[...]]`.
- `[[payment_link]]` and `[[invoice_link]]` contain a link to the portal. The link needs the `app_url` setting, and the customer must log in first.
- Use the same `GOBILL_SECRET_KEY` that production will use. Or let gobill create `<db>.key`, then back that file up.

## What is converted

Imported: settings, admins, routers, bandwidth, IP pools, plans, customers, active subscriptions, transactions, vouchers, logs, and NAS.

- Admin sha1 passwords are marked `legacy_sha1`, and changed to bcrypt at the first login.
- Customer passwords and device secrets are encrypted again, with bcrypt for passwords and AES-GCM for secrets.
- Expiry times are converted to Unix UTC. Money becomes an INTEGER amount in rupiah.
- A price that cannot be converted is skipped, and reported.
- The `RadiusRest` plan becomes a `Radius` plan. See [FreeRADIUS over REST](freeradius-rest.md).

Not imported yet: coupons, ODP, and inbox.

### Customer attributes

Customer attributes in `tbl_customers_fields` are imported as custom fields with the same name. The fields appear on the customer form. gobill uses them the same way PHP does:

- `<name> Bill`, for example `Router Bill`: an extra bill. Its value is added to the price of each recharge, from admin, balance, voucher, gateway, or auto-renew. A value in the format `amount:remaining`, for example `50000:3`, is an installment. The remaining count drops by one at each recharge, and stops at 0.
- `Invoice`: the price of the next bill for a Period plan. This value replaces the plan price. It is filled in automatically after a Period recharge, as in PHP.
- `Expired Date`: the due date of a Period plan for each customer. The value goes to the customer's `billing_day`, not to a custom field. A value that is not a number from 1 to 31 is skipped and reported.

Difference: an online gateway payment uses the plan price plus the bills when the order is created. A coupon only discounts the plan price. The bills are added on top.

### Fields that are dropped on purpose

`account_type`, city, district, province, postal code, `price_old`, and `plan_type`.

## Verification already done

The import was tested with a real production dump. All expiry dates and transaction totals matched the old system. Repeat this comparison with your latest dump before the cutover.

## WhatsApp after cutover

In the old system, `wa_url` points to the PHP application itself, for example `http://<php-host>/?_route=plugin/wga_sendMessage&phone=[[phone]]&message=[[text]]&secret=...`. The "Alternative WhatsApp Gateway" plugin forwards it to the WA server. After cutover, the PHP application no longer exists, so that `wa_url` must not be kept.

`gobill import` also brings `alt_wga_server_url`, `alt_wga_device_id`, `alt_wga_username`, and `alt_wga_password`. gobill uses them to send directly to the WA server. While `alt_wga_server_url` has a value, `wa_url` is ignored.

After the import:

1. Open **Settings > Integrations**. Check that the four fields under "WhatsApp (WA server)" are filled in. The password shows empty, which is normal.
2. Clear the `wa_url` that still points to the PHP plugin, then save.
3. Make sure the WA server can be reached from the new STB. The address `127.0.0.1` is correct only if the WA server runs on the same STB.
4. Click "Send test message" to your own number.

See [integrations](integrations.md#whatsapp).

## Parallel run checklist

Before a parallel run, set `notify_customers` = `no` in **Settings > Notifications**. This keeps customers from receiving messages from two systems at once. Operator alerts keep running. See [integrations](integrations.md#message-switches).

1. Test CoA Disconnect on MikroTik, after the NAS-IP-Address fix.
2. Test the FreeRADIUS REST path. Point the test server's `connect_uri` to gobill in the test instance, then run `freeradius -X`.
3. Test hotspot voucher login over RADIUS, and the MAC limiter.
4. Import the latest production dump to the STB. Compare active customers, expiry dates, and balances with the old system.
5. Run in parallel for 1 to 3 days in read-only mode, that is, accounting only. Compare sessions and expiry dates.
6. Build for ARM and test on the STB: RAM, start without an RTC, power cuts, and backup to USB.
7. Clean up test leftovers on the router. See [MikroTik setup](mikrotik.md#lessons-from-field-testing).

## One STB shared with PHPNuxBill, FreeRADIUS, and the WA server

If gobill is installed on an STB that already runs PHPNuxBill, FreeRADIUS, and the WA server, for example at `192.168.99.2`, nothing needs to be installed again for WhatsApp. The WA server stays in use. gobill only sends to it. The thing to watch is port conflicts.

1. **HTTP port.** gobill's default is `:8080`. If another application uses that port, set `GOBILL_HTTP` to another port in `/etc/gobill/config.env`, for example `GOBILL_HTTP=:8090`. Check first with `ss -ltn`.
2. **RADIUS port.** FreeRADIUS already uses UDP 1812 and 1813. As long as FreeRADIUS remains the RADIUS server through `/radius.php`, set `GOBILL_RADIUS=off`. If MikroTik is later pointed directly at gobill, stop FreeRADIUS first, then enable gobill's RADIUS.
3. **`/radius.php` allow-list.** FreeRADIUS on the same host calls through loopback. Loopback is allowed by an empty `radius_rest_allow`. There is no need to fill it in.
4. **WA server.** The `alt_wga_server_url` from the import, `http://127.0.0.1:3030`, is correct directly because the WA server is on the same STB. Clear the `wa_url` that contains the PHP plugin, then send a test message to your own number.
5. **Notifications during the parallel run.** `notify_customers` = `no` until cutover. PHPNuxBill keeps sending messages to customers.

## Cutover and rollback

### Cutover

1. Stop changes in the old system for a short window. Take the latest JSON backup, or a MySQL dump.
2. Import into gobill, through the UI or with `gobill import --json=...`. Check the report. Make sure `notify_customers` is still `no`.
3. Move RADIUS. Change the FreeRADIUS `connect_uri` to gobill, or change `address` and `secret` on the MikroTik `/radius` entry to gobill.
4. Turn off notifications in PHPNuxBill, or stop its cron job. Then set `notify_customers` = `yes` in gobill. This order prevents duplicate messages.
5. Watch the RADIUS log, active sessions, and the System Status page for a few hours.

### Rollback

Change the `connect_uri` or the `/radius` address back to the old entry. The old system's data does not change. However, transactions that happened in gobill after the cutover do not come back. Record them, and enter them by hand if needed.

## See also

- [Installation](installation.md): installing gobill.
- [Integrations](integrations.md): WhatsApp, message switches, and gateways.
- [MikroTik setup](mikrotik.md): RADIUS on the router.
- [FreeRADIUS over REST](freeradius-rest.md): the REST path.
- [Backup and restore](backup-restore.md): import safety and restore.
