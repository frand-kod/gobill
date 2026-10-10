# Installation

This document explains how to install gobill on an Armbian STB, a Linux VPS, or Docker. gobill is a single binary, with no PHP and no separate web server.

**For:** operators

**Prerequisites:** `root` or `sudo` access to the device, a working network connection, and for an STB, the official Armbian image for your board.

## Armbian STB

### 1. Install Armbian

1. Write the official Armbian image to an SD card or eMMC, for example with Balena Etcher or `dd`.
2. Boot the device with a working network connection.
3. Log in as `root` or a `sudo` user, then run:

        apt update && apt -y upgrade

### 2. Install gobill

Get `install.sh` from the release, or use `deploy/install.sh` from the repository. Then run one of the following commands.

From a binary you have already downloaded:

    sudo sh install.sh ./gobill-linux-arm64

Directly from the release URL. `{arch}` is replaced automatically with `arm64`, `armv7`, or `amd64`:

    sudo sh install.sh https://github.com/frand-kod/gobill/releases/latest/download/gobill-linux-{arch}

The script:

- detects the architecture and installs the binary to `/usr/local/bin/gobill`;
- creates the system user `gobill` and the folder `/var/lib/gobill`;
- creates `/etc/gobill/config.env` with a random `GOBILL_SECRET_KEY`, only if the file does not exist yet, with mode 600;
- enables chrony or systemd-timesyncd if present;
- enables and starts the `gobill` service.

The script is safe to run again. To remove the service and binary without deleting data:

    sudo sh install.sh --uninstall

The first admin password is stored in the file `initial-admin-password.txt` in the database folder. The file uses mode 0600, and the password is not written to the log. For a default installation:

    sudo cat /var/lib/gobill/initial-admin-password.txt

Then:

1. Open `http://STB-IP:8080`.
2. Log in as `admin`.
3. Change the password.
4. Delete the initial password file:

        sudo rm /var/lib/gobill/initial-admin-password.txt

### 3. Clock and NTP

STBs usually have no RTC. After boot, the clock can return to 1970 or to the image build time until NTP syncs. For that reason, gobill uses a clock guard.

- The service waits for `time-sync.target` and `network-online.target` before it starts.
- The expiry job refuses to run if the clock is earlier than the last time recorded in the database, or if NTP is not yet synced. The refusal is logged and shown as a banner on the dashboard.
- Without this guard, a clock that jumps forward could expire all customers at once.
- If the device has an accurate RTC, turn the guard off with the setting `clock_guard` = `off`. See [configuration](configuration.md).

Check synchronization:

    timedatectl status
    chronyc tracking

The first line of `timedatectl status` must show `System clock synchronized: yes`. Run `chronyc tracking` only if you use chrony.

### 4. Backup to USB or NAS

The data is in `/var/lib/gobill`: `gobill.db` and `gobill.db.key`. Store backups outside the eMMC or SD card, because both wear out quickly and are at risk during power cuts. The full steps are in [backup and restore](backup-restore.md).

### 5. Firewall ports

| Direction | Port | Protocol | Notes |
|---|---|---|---|
| MikroTik to STB | 1812 | UDP | RADIUS authentication |
| MikroTik to STB | 1813 | UDP | RADIUS accounting |
| STB to MikroTik | 3799 | UDP | CoA: disconnect sessions and change plans |
| STB to MikroTik | 8728 | TCP | RouterOS API. Use 8729 if TLS is used |
| Admin LAN to STB | 8080 | TCP | Admin UI. Do not open to the internet |

A complete firewall rule example is in [security](security.md#firewall).

## VPS

The steps are the same as for the STB, with the `amd64` binary. There are three important differences.

- The VPS clock is already synced, so the clock guard rarely matters.
- Do not send RADIUS UDP or CoA over the open internet. Use WireGuard or RadSec. See [security](security.md#remote-link-vps).
- Put a reverse proxy with HTTPS in front of `:8080`. Leave `GOBILL_HTTPS` unset so the cookie stays `Secure`. Set the setting `trust_proxy` = `yes`. If the proxy does not run on the same host, set `trusted_proxies` to the proxy IP. See [configuration](configuration.md#network-and-proxy).

## Docker

    docker build --build-arg VERSION=dev -t gobill .
    docker run -d --name gobill -p 8080:8080 -p 1812:1812/udp -p 1813:1813/udp \
      -v gobill-data:/data gobill

The SQLite database and `gobill.db.key` are in the `/data` volume. To manage the key yourself, add `-e GOBILL_SECRET_KEY=<SECRET>`.

The first admin password is in `/data/initial-admin-password.txt`:

    docker exec gobill cat /data/initial-admin-password.txt

After you log in and change the password, delete that file:

    docker exec gobill rm /data/initial-admin-password.txt

## Without the installer

Run the binary directly:

    GOBILL_DB=./gobill.db ./gobill

Or run `go run ./cmd/gobill` from the repository root.

Manual build for the STB:

    CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags="-s -w" -o gobill ./cmd/gobill
    CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 go build -trimpath -ldflags="-s -w" -o gobill ./cmd/gobill

For development, see the [developer guide](../internal/pengembangan.md). The developer guide is in Indonesian only.

## Troubleshooting

Read the service logs:

    journalctl -u gobill -n 100 --no-pager
    journalctl -u gobill -f
    systemctl status gobill

| Symptom | Cause and fix |
|---|---|
| The log contains `clock`, and expiry is refused | The clock is not synced yet. Check `timedatectl status`, and make sure the internet is reachable. |
| The log contains `first admin created` | The first admin was created because the database is new. The password is in `initial-admin-password.txt` in the database folder. |
| The log contains `GOBILL_RADIUS ... want host:port` | The format is wrong. Use `:1812`, or `off` to turn the listener off. |
| `permission denied` when writing the database | Run `sudo chown -R gobill:gobill /var/lib/gobill`. |
| The service keeps restarting | Read the log. Make sure `/etc/gobill/config.env` exists and contains `GOBILL_SECRET_KEY`. |
| Customers cannot log in to hotspot | Run `journalctl -u gobill \| grep -i radius`. Make sure the secret matches and port 1812 is not blocked. |
| Port 8080 or 1812 is already in use | Check with `ss -ulnp \| grep 1812` or `ss -tlnp \| grep 8080`. Change `GOBILL_HTTP` or `GOBILL_RADIUS`. If FreeRADIUS uses 1812 on the same host, set `GOBILL_RADIUS=off`. `/radius.php` keeps running. |

## See also

- [Upgrade](upgrade.md): replace the binary with a new version.
- [Configuration](configuration.md): all `GOBILL_*` variables and settings.
- [MikroTik setup](mikrotik.md): connect the router.
- [Security](security.md): RADIUS hardening, firewall, and admin 2FA.
- [Monitoring](monitoring.md): monitor from outside and operator alerts.
- [Backup and restore](backup-restore.md): backup, mirror, and recovery.
- [Migrating from PHPNuxBill](migration-phpnuxbill.md): move data from the old system.
