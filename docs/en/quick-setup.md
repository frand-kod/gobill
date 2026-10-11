# Quick setup

Quick setup guides you through installing gobill one step at a time, like an operating system installer. Each step shows only the fields it needs, and a step that already has its data is marked done automatically. Quick setup is shown to SuperAdmin accounts only.

**For:** operators

**Prerequisites:** a SuperAdmin account, a MikroTik router that gobill can reach, and (optional) a PHPNuxBill backup file.

## When it appears

When a SuperAdmin logs in for the first time, gobill shows a dialog with three choices:

- **Start quick setup**: open the first step.
- **Import from PHPNuxBill**: open the import page. After a successful import, each step shows the imported data for you to check, instead of creating new data.
- **Skip**: the dialog does not appear again.

While it is not finished and not skipped, **Quick setup** is at the top of the sidebar with a progress badge, for example `3/9`. The badge counts the required steps that already have data.

## Steps

The steps, in order:

1. **Welcome**: start from scratch, or import from PHPNuxBill.
2. **Business and app URL**: business name, app URL, and phone number. These are the same fields as **Settings > General**.
3. **Router or NAS**: choose how gobill connects to MikroTik, then fill in the matching form (see below).
4. **Speed**: three presets, 5, 10, and 20 Mbps. Change the values, or leave a field empty to skip it.
5. **Plans**: daily, weekly, and monthly templates with suggested prices. You can change the prices. Plans use the speed and router you chose.
6. **WhatsApp**: enter the GOWA gateway address, then send a test message.
7. **QRIS**: upload the static QRIS image. The decoded preview must scan with a banking app.
8. **Tripay (optional)**: Tripay API keys, if you use Tripay.
9. **Isolation (optional)**: turn isolation on, then copy the MikroTik commands it shows.
10. **First customer or voucher**: create one test voucher for the plan you choose.
11. **Finish**: a final test. Log in on the hotspot with the test voucher. The indicator turns green when the router reports that session.

Each step has a **Learn more** section with a link to the related guide. Use **Back** to return to the previous step, and **Skip this step** to continue without saving.

## Router or NAS

Choose one of the following.

| Choice | Use for | What is created |
|---|---|---|
| RADIUS (recommended) | Hotspot and PPPoE login through RADIUS | A NAS with name, IP, and secret |
| API | gobill reads and controls the router directly | A router with host, port, and login |
| Both | Both of the above | A NAS and a router |

For RADIUS, gobill shows the RouterOS commands with the gobill IP and the secret filled in. Paste them into the MikroTik terminal. The secret is shown only right after it is saved. If you open this step again, replace `<secret>` with your NAS secret.

The **Waiting for a packet from the router** indicator turns green when the router sends its first RADIUS packet. For API, the **Test connection** button saves the router and then checks the connection.

If a NAS or router with the same IP or host already exists, this step edits it. It does not create a second one.

## Data it creates

Quick setup has no data of its own. Each save uses the same page and rules as the normal menus:

| Step | Normal page |
|---|---|
| Business and app URL | **Settings > General** |
| Router or NAS | **Routers** and **NAS** |
| Speed | **Bandwidth** |
| Plans | **Service Plan** |
| WhatsApp and QRIS | **Settings > Integrations** and **Settings > Payments** |
| Tripay and Isolir | **Settings > Payments** and **Settings > Isolation** |

You can change any value on those pages at any time.

## After it is finished

On the last step, press **Open the dashboard**. gobill saves the finished state, and **Quick setup** disappears from the sidebar. To open it again, choose **Guide**, then **Quick setup**.

If you pressed **Skip** in the dialog, the menu stays hidden. Open `/admin/setup` directly in the browser to use it.

## See also

- [Installation](installation.md): install gobill before you run quick setup.
- [MikroTik setup](mikrotik.md): details of the RADIUS and API commands.
- [Integrations](integrations.md): WhatsApp, QRIS, and Tripay.
- [Isolation](isolation.md): the isolir page and the MikroTik commands.
- [Migration from PHPNuxBill](migration-phpnuxbill.md): import the data from PHPNuxBill.
