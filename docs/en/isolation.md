# Isolation of expired customers

Isolation keeps customers whose package has run out online at a restricted speed instead of cutting them off at once. They move to the **Isolir** plan (64 kbps) for a set number of days. On this plan the internet still works slowly, enough to open WhatsApp and banking apps. When they open other sites, they are redirected to the isolation page. It shows the package, a **Contact admin** button, an optional logout button and a voucher form. The isolation page offers no payment: the customer pays through the admin or the portal.

Isolation only works for customers who log in through gobill's RADIUS (plans on the **Radius** device). Customers whose accounts live directly on a MikroTik (local hotspot or PPPoE users) are cut off as before.

## Settings

Open **Settings > Isolation**. This tab is for SuperAdmin only. Each field shows its default while it has never been saved. Each section has a **Restore default** button.

| Setting | Default | Notes |
|---|---|---|
| Isolation enabled | No | Turn on after the router has been set up (see below). |
| Plan name | Automatic | System plan used by isolation. gobill creates the **Isolir** plan the first time isolation is enabled, with the **Isolir 64k** bandwidth profile. The plan is not sold in the portal and cannot be given manually. |
| Download speed, Upload speed | `64k` | Speed of the Isolir plan, for example `64k` or `2M`. Saved to the **Isolir 64k** bandwidth profile. |
| Isolation duration (days) | 30 | How many days isolation lasts after the package ends. Then the customer is inactive, as before. `0` means isolated until the customer pays. |
| Title | Your internet package has run out | Title of the isolation page. |
| Message | Please pay to reactivate. Internet still works slowly so you can open WhatsApp and banking apps. | Text under the title. |
| Admin WhatsApp number | Company phone | Used by the **Contact admin** button. |
| Show voucher form | Yes | Lets the customer enter a voucher on the isolation page. |
| MikroTik address-list name | `isolir` | Address-list name sent by RADIUS to isolated customers. |
| Hotspot address | Empty | Address customers use for the hotspot login page, for example `http://fourkeys.net` or `http://192.168.20.1`. Empty means no logout button. |
| Display mode | Default | `Custom HTML` uses the HTML in the field below. |

**Log out / use another account.** When the Hotspot address is set, the isolir page shows a **Log out / use another account** button that links to `<Hotspot address>/logout`. The hotspot logout clears the login cookie on the customer's device, so they can sign in with another account. The router must not redirect that address, see the `dst-address-type=!local` rule in [MikroTik setup](mikrotik.md#isolir).

## Behaviour

1. The package runs out. The expiry notice is still sent as usual.
2. If isolation is on and the package has no explicit **expired plan** (the plan setting `expired_plan_id`), the customer moves to the Isolir plan for **Isolation Days**. If the plan has an explicit expired plan, that setting is used.
3. A customer who opens the internet is redirected to `/isolir`. The page recognises the customer by the client IP of their open RADIUS session.
4. A paid recharge or a voucher ends the isolation. The customer's sessions are dropped on the router, and they log in again to get their normal package.
5. When the isolation days run out, the customer is inactive, as before.

Isolation does not renew the Isolir plan automatically. A customer with enough balance is still renewed from their original plan, and no isolation starts.

## MikroTik router setup

The **Isolation** tab shows the RouterOS commands in the **MikroTik router setup** section, filled in with gobill's LAN address and port. The rules, the hotspot bypass and the curl test are in [MikroTik setup](mikrotik.md#isolir).

## Custom HTML

In **Custom HTML** mode, the HTML you write is shown after its variables are replaced:

| Variable | Value |
|---|---|
| `[[company_name]]` | Company name |
| `[[logo_url]]` | Logo address |
| `[[title]]`, `[[message]]` | Isolation title and message |
| `[[username]]`, `[[name]]` | Customer username and name |
| `[[plan_name]]`, `[[expired_at]]`, `[[amount]]` | Last package, end time and price |
| `[[logout_url]]` | Hotspot logout address (`<Hotspot address>/logout`), empty when the Hotspot address is not set |
| `[[wa_admin_link]]` | Chat-admin link |
| `[[voucher_form]]` | Ready-made voucher form |
| `[[promos]]` | Promo blocks |
| `[[known]]` | `yes` when the customer is recognised, otherwise `no` |
| `[[notice]]` | Result message of a voucher attempt |

Click a variable below the HTML box to insert it at the cursor. **Back to default** deletes the custom HTML. **Restore default** in the **Display mode** section only switches the mode back, and keeps the HTML.

Values other than `voucher_form` and `promos` are HTML-escaped. Unknown or empty variables become empty. The HTML runs in a sandbox with no access to gobill's cookies, so scripts in it cannot read the admin session. Blank HTML falls back to the default page.

Use **Open preview** to see the page with sample data. The preview uses the saved HTML, so save your changes first.

## Limits

- Isolation only applies to customers who log in through gobill's RADIUS.
- The customer is recognised from the framed IP of the most recent open RADIUS session. Make sure the NAS sends accounting correctly.
- The voucher form on the isolation page allows 10 wrong attempts per IP in 15 minutes.
