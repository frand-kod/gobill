# Backup and restore

This document explains how to create NuxBill backups, keep copies outside the device, and restore the database. Daily backups are created automatically. A second copy, the mirror, can be kept on USB, NAS, or Google Drive through rclone.

**For:** operators

**Prerequisites:** NuxBill is installed. For a mirror, the mirror destination is mounted and writable. See [installation](installation.md).

## Daily backups

The data is in `/var/lib/nuxbill`:

- `nuxbill.db`: the SQLite database.
- `nuxbill.db.key`: the encryption key.

Daily backups are created automatically with `VACUUM INTO`. The destination folder is set by `NUXBILL_BACKUP_DIR`. The number of files kept is set by the `backup_keep` setting, default 7.

To use another folder, for example on USB:

1. Mount the storage, for example at `/mnt/usb`. Use the fstab option `nofail`, so boot does not hang if the USB is not attached.
2. Create a service drop-in:

        sudo systemctl edit nuxbill

   Put this in it:

        [Service]
        ReadWritePaths=/mnt/usb/nuxbill-backup

3. Add this to `/etc/nuxbill/config.env`:

        NUXBILL_BACKUP_DIR=/mnt/usb/nuxbill-backup

4. Restart the service:

        sudo systemctl restart nuxbill

## Mirror

Keep backups outside the SD card. If the SD card or the STB fails, the backups in `NUXBILL_BACKUP_DIR` are lost too. A mirror is a second copy in another folder.

How it works:

- Set `NUXBILL_BACKUP_MIRROR` to a second folder.
- Each daily backup is copied to that folder right away.
- The mirror is trimmed with the same `backup_keep` value.
- The folder must already exist. NuxBill does not create it, so a disk that is not mounted cannot write to the SD card.
- If a copy fails, an alert is sent once when the mirror starts failing, and once when it recovers. The alert uses the channel in `alert_channel`. The local backup is still created.
- A failed mirror copy is retried every hour.
- The last status is shown in **Settings > Miscellaneous**, below "Download backup".

### Mirror destination options

**USB stick.** Mount it at `/mnt/usb` with the fstab option `nofail`, as above. Set:

    NUXBILL_BACKUP_MIRROR=/mnt/usb/nuxbill-mirror

**NAS (NFS or SMB).** Create a systemd mount unit, so the mount waits for the network. Example NFS unit in `/etc/systemd/system/mnt-nas.mount`:

    [Unit]
    Description=NAS backup
    Wants=network-online.target
    After=network-online.target

    [Mount]
    What=192.168.1.10:/volume1/nuxbill
    Where=/mnt/nas
    Type=nfs
    Options=_netdev,soft,timeo=50

    [Install]
    WantedBy=multi-user.target

Then enable the unit:

    sudo systemctl enable --now mnt-nas.mount

For SMB, change `Type=cifs`, and add `credentials=/etc/nuxbill/smb.cred` to `Options`. The unit file name must match the mount path. For example, `/mnt/nas` becomes `mnt-nas.mount`.

Then add a service drop-in with `sudo systemctl edit nuxbill`:

    [Unit]
    RequiresMountsFor=/mnt/nas

    [Service]
    ReadWritePaths=/mnt/nas/nuxbill-backup

`RequiresMountsFor=` makes NuxBill wait until the mount is ready. Set `NUXBILL_BACKUP_MIRROR=/mnt/nas/nuxbill-backup`. Create that folder once by hand on the NAS.

**rclone to Google Drive.** Mount it with:

    rclone mount gdrive: /mnt/gdrive --vfs-cache-mode writes --daemon

You can also run it as a systemd unit, with `RequiresMountsFor=/mnt/gdrive` and `After=network-online.target`. Set `NUXBILL_BACKUP_MIRROR=/mnt/gdrive/nuxbill-backup`, and add `ReadWritePaths=/mnt/gdrive/nuxbill-backup` in the drop-in. Google Drive has quotas and pauses, so check that folder now and then.

After you set `NUXBILL_BACKUP_MIRROR` in `/etc/nuxbill/config.env`, restart NuxBill.

### Test a restore from the mirror

Run this test once after the mirror is first set up, and each time the mirror destination changes. Do not wait for a disaster.

1. Copy the newest file from the mirror folder to `/tmp/uji.db`. Also copy the matching `nuxbill.db.key`. Without the key, the data cannot be read.
2. Run a separate instance on another port, for example `NUXBILL_DB=/tmp/uji.db` and `NUXBILL_HTTP=:8099`.
3. Check the customer data in the UI.

## Key backup with the database

Back up `nuxbill.db.key` together with the database. Without this file, encrypted data, such as device and gateway passwords, cannot be read.

If you use `NUXBILL_SECRET_KEY`, store its value safely. That value replaces the `.key` file.

Manual copy:

    sudo cp /var/lib/nuxbill/nuxbill.db /var/lib/nuxbill/nuxbill.db.key /mnt/usb/manual-backup/

## Restore the database

A SuperAdmin can restore a gobill backup file (`.db`) from **Settings > Miscellaneous > Restore database**. The page is at `/admin/settings/miscellaneous/restore`.

The restore flow:

1. The application checks the file: integrity, a schema version that is not newer than this application, and the encryption key.
2. The application shows the number of records in the backup, compared with the current data.
3. After confirmation, the current data is backed up automatically to the backup folder, with a name that ends in `-pre-restore.db`.
4. The backup file is installed as `nuxbill.db.restore`.
5. The application exits with code 3. systemd with `Restart=on-failure` starts it again.
6. At start, the old database is moved next to the database file, named `nuxbill.db.pre-restore-<time>-<random>`. Its `-wal` file is moved with it.
7. The backup is applied before the database opens. A backup from an older version is upgraded through migrations.

After you are sure the restore worked, you can delete the `nuxbill.db.pre-restore-*` files. The automatic backup is already kept.

A backup must be made with the same `NUXBILL_SECRET_KEY` or `nuxbill.db.key` as the target application. If the keys differ, the restore is refused.

### Manual restore

Use these steps if the application does not come back, or does not run under systemd.

1. Stop the service.
2. Replace the database file.
3. Delete the `-wal` and `-shm` files. The old `-wal` file must be deleted. Otherwise its content is replayed onto the restored database.
4. Start the service again.

    sudo systemctl stop nuxbill
    sudo cp /mnt/usb/manual-backup/nuxbill.db /var/lib/nuxbill/nuxbill.db
    sudo rm -f /var/lib/nuxbill/nuxbill.db-wal /var/lib/nuxbill/nuxbill.db-shm
    sudo systemctl start nuxbill

## Import safety

Before an import of data from PHPNuxBill, NuxBill creates an automatic database backup in `NUXBILL_BACKUP_DIR`. The name is `nuxbill-YYYYMMDD-HHMMSS-pre-import.db`. If the backup fails, the import does not run. The import steps are in [migration](migration-phpnuxbill.md).

## See also

- [Installation](installation.md): setup and data folder.
- [Upgrade](upgrade.md): backups before an upgrade, and rollback.
- [Configuration](configuration.md): `NUXBILL_BACKUP_DIR`, `NUXBILL_BACKUP_MIRROR`, and `backup_keep`.
- [Monitoring](monitoring.md): backup and mirror alerts.
- [Migrating from PHPNuxBill](migration-phpnuxbill.md): import and import safety.
