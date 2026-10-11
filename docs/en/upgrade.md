# Upgrade

This document explains how to replace the gobill binary with a new version without changing data or configuration. `sudo gobill update` is the recommended way. The manual method stays as a fallback. The last section has version notes for changes that need action.

**For:** operators

**Prerequisites:** root access (`sudo`) to the device. `gobill update` needs internet access to GitHub. Keep a copy of `gobill.db.key` outside the device. See [backup and restore](backup-restore.md).

## Upgrade with `gobill update`

Run as root:

        sudo gobill update

The command does the following steps.

1. Fetches the latest release info from GitHub. If the version is the same as the running one, the command stops with `already up to date`.
2. Shows the old and new versions, and the first lines of the release notes. The command asks for confirmation, unless you pass `--yes`.
3. Downloads the binary for the device architecture and the `SHA256SUMS` file. The checksum must match. If it does not match, or the checksum file is missing, the install stops and nothing changes.
4. Creates a database backup with `VACUUM INTO` in the backup folder, named `gobill-YYYYMMDD-HHMMSS-pre-update.db`. The path is shown. The daily backup rotation does not delete this file.
5. Keeps the old binary as `/usr/local/bin/gobill.prev`, then replaces the binary with the new version.
6. Restarts the service, then checks `/health` for up to 60 seconds.
7. If the check fails, the command puts back the old binary and the database from the backup, starts the service again, and exits with an error.

Options:

| Option | Purpose |
|---|---|
| `--version vX.Y.Z` | Install a specific release. Also used to go back to an older version. |
| `--yes` | Skip the confirmation. |

Builds without a release tag, such as `dev` builds, are not updated automatically. Use `--version` to install a release.

After the update, check the version on the **System Status** page and confirm that the application runs normally. See [monitoring](monitoring.md).

## Roll back with `gobill rollback`

        sudo gobill rollback

The command swaps the binary with `/usr/local/bin/gobill.prev`, then starts the service. Run the same command again to go forward to the new version.

If the last update ran migrations, the new database does not match the old binary. The command then shows the pre-update backup recorded during the update, and asks before it restores that backup. Answer `y` only if you are sure. Data written after that backup is lost. If you answer `n`, the command shows the steps to restore later.

## Manual upgrade (fallback)

Use this method if `gobill update` cannot be used, for example when the device has no access to GitHub.

1. Create a backup of the database and the key file. Store it outside the device.
2. Install the new binary. With `install.sh`:

        sudo sh install.sh ./gobill-linux-arm64

   Or manually:

        sudo systemctl stop gobill
        sudo install -m 0755 ./gobill-linux-arm64 /usr/local/bin/gobill
        sudo systemctl start gobill

3. Check the version:

        /usr/local/bin/gobill --version

4. Open the **System Status** page and check that the application runs normally. See [monitoring](monitoring.md).

Data and configuration do not change. Database migrations run automatically at start.

## Manual rollback (fallback)

Migrations run automatically at start, so replacing the binary alone is not enough. Use these steps:

1. Stop the service:

        sudo systemctl stop gobill

2. Install the old binary.
3. Restore the database from the backup made before the upgrade. Follow "Manual restore" in [backup and restore](backup-restore.md#manual-restore).
4. Start the service:

        sudo systemctl start gobill

## Update check

The `update_check` setting is in **Settings > System & Security**, in the **System** section. The default is **Yes**.

When it is on, gobill checks the latest release on GitHub a few minutes after start, and then once a day. If a newer release exists, SuperAdmin sees a `vX.Y.Z available` badge in the header. The badge links to the release page and to this upgrade guide. Choose **No** to turn the check off. If the check fails, gobill only writes it to the log.

## Version notes

Before you upgrade, read the behaviour changes section ("Perubahan perilaku") of the [CHANGELOG](../../CHANGELOG.md) for the target version. The CHANGELOG is written in Indonesian. Its behaviour changes list what the operator must act on.

**Upgrade from v0.1.6 or older to v0.1.7:**

1. `gobill update` only works when the release includes a `SHA256SUMS` file. Older releases do not have that file, so the first upgrade to v0.1.7 uses the manual method. After that, `sudo gobill update` works.
2. Customer login moved to `/login`, and admin login moved to `/admin/login`. Update your bookmarks.
3. Set `GOBILL_HTTP` to the LAN IP and port, for example `192.168.99.2:8090`, in `/etc/gobill/config.env`. The isolir commands in **Settings > Isolation** use this address.
4. Backup, import, and restore are in **Settings > System & Security**.
5. Migration 0018 runs automatically at start. To go back to an older version after the migration has run, restore the pre-update backup that was made during the upgrade. See [rollback](#roll-back-with-gobill-rollback).
6. Customer phone numbers are normalised to the `62…` format at start.

The full list of changes is in the [CHANGELOG](../../CHANGELOG.md).

## See also

- [Installation](installation.md): first-time installation.
- [Backup and restore](backup-restore.md): create and restore backups.
- [Monitoring](monitoring.md): monitor the application after an upgrade.
- [Migrating from PHPNuxBill](migration-phpnuxbill.md): move from the old system.
