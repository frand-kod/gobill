# Upgrade

This document explains how to replace the NuxBill binary with a new version without changing data or configuration. The last section has notes for each version that needs special action, and explains how to roll back.

**For:** operators

**Prerequisites:** backups of `nuxbill.db` and `nuxbill.db.key` exist. See [backup and restore](backup-restore.md).

## Upgrade steps

1. Create a backup of the database and the key file. Store it outside the device.
2. Install the new binary. With `install.sh`:

        sudo sh install.sh ./nuxbill-linux-arm64

   Or manually:

        sudo systemctl stop nuxbill
        sudo install -m 0755 ./nuxbill-linux-arm64 /usr/local/bin/nuxbill
        sudo systemctl start nuxbill

3. Check the version:

        /usr/local/bin/nuxbill --version

4. Open the **System Status** page and check that the application runs normally. See [monitoring](monitoring.md).

Data and configuration do not change. Database migrations run automatically at start.

## Upgrade from v0.1.3 to v0.1.4

Migrations 0011 to 0014 run automatically at start. They add the `start_on_first_login` column, indexes, customer snapshots on payments, and admin 2FA.

Before you upgrade, check the following.

- If FreeRADIUS runs on another host, set `radius_rest_allow` to its IP. An empty value now means loopback only.
- If the UI is reached over plain HTTP on a LAN IP, set `NUXBILL_HTTPS=0` in `config.env`. By default the cookie uses the `Secure` flag, so login fails without this setting.
- Behind a reverse proxy, set `trust_proxy` and `trusted_proxies`. See [configuration](configuration.md#network-and-proxy).
- Monitors that read the version or free disk space from `/health` must move to `/metrics` or `/admin/status.json`. See [monitoring](monitoring.md).

The full list of changes is in the [CHANGELOG](../../CHANGELOG.md).

## Rollback

Migrations run automatically at start, so replacing the binary alone is not enough. Use these steps:

1. Stop the service:

        sudo systemctl stop nuxbill

2. Install the old binary.
3. Restore the database from the backup made before the upgrade. Follow "Manual restore" in [backup and restore](backup-restore.md#manual-restore).
4. Start the service:

        sudo systemctl start nuxbill

## See also

- [Installation](installation.md): first-time installation.
- [Backup and restore](backup-restore.md): create and restore backups.
- [Monitoring](monitoring.md): monitor the application after an upgrade.
- [Migrating from PHPNuxBill](migration-phpnuxbill.md): move from the old system.
