# Isolation of expired customers

Isolation keeps customers whose package has run out online at a restricted speed instead of cutting them off at once. They move to the **Isolir** plan (64 kbps) for a set number of days. On this plan the internet still works slowly, enough to open WhatsApp and banking apps. When they open other sites, they are redirected to the isolation page with the bill, a QRIS code, a chat-admin button and a voucher form.

Isolation only works for customers who log in through gobill's RADIUS (plans on the **Radius** device). Customers whose accounts live directly on a MikroTik (local hotspot or PPPoE users) are cut off as before.

## Settings

Open **Settings → Isolir**. This tab is for SuperAdmin only. Each field shows its default while it has never been saved. Each section has a **Restore default** button.

| Setting | Default | Notes |
|---|---|---|
| Isolation Enabled | No | Turn on after the router has been set up (see below). |
| Isolir Plan | Automatic | When isolation is first enabled, gobill creates the **Isolir** plan (bandwidth **Isolir 64k**, 64 kbps up/down). It is not sold in the portal. The admin may pick another plan. |
| Isolation Days | 30 | How many days isolation lasts after the package ends. Then the customer is inactive, as before. `0` means isolated until the customer pays. |
| Title | Your internet package has run out | Title of the isolation page. |
| Message | Please pay to reactivate. Internet still works slowly so you can open WhatsApp and banking apps. | Text under the title. |
| Admin WhatsApp number | Company phone | Used by the "Paid? Chat admin" button. |
| Show QRIS | Yes | QRIS locked to the last package price. Needs a static QRIS under Payment. |
| Show voucher form | Yes | Lets the customer enter a voucher on the isolation page. |
| MikroTik address-list name | `isolir` | Address-list name sent by RADIUS to isolated customers. |
| Display mode | Default | `Custom HTML` uses the HTML in the field below. |

## Behaviour

1. The package runs out. The expiry notice is still sent as usual.
2. If isolation is on and the package has no explicit **expired plan** (the plan setting `expired_plan_id`), the customer moves to the Isolir plan for **Isolation Days**. If the plan has an explicit expired plan, that setting is used.
3. A customer who opens the internet is redirected to `/isolir`. The page recognises the customer by the client IP of their open RADIUS session.
4. A paid recharge or a voucher ends the isolation. The customer's sessions are dropped on the router, and they log in again to get their normal package.
5. When the isolation days run out, the customer is inactive, as before.

Isolation does not renew the Isolir plan automatically. A customer with enough balance is still renewed from their original plan, and no isolation starts.

## MikroTik router setup

The Isolir tab shows commands you can copy. The gobill address comes from **App URL**, or from the address you use to open the admin pages when App URL is empty. Replace the IP if gobill's LAN address differs.

Example (gobill at `192.168.88.2`, port `8080`):

```
/ip firewall nat add chain=dstnat src-address-list=isolir protocol=tcp dst-port=80 action=dst-nat to-addresses=192.168.88.2 to-ports=8080
```

This NAT rule redirects HTTP from isolated clients to the gobill page.

```
/ip firewall filter add chain=forward src-address-list=isolir dst-address=192.168.88.2 action=accept
```

This filter rule lets isolated clients reach gobill. Place it above the drop rule for isolated clients. On RouterOS 6 or 7, use `place-before` if the order needs adjusting.

```
/ip hotspot walled-garden add dst-host=billing.example.com action=allow
```

The walled garden lets the isolation page open before hotspot login. If gobill uses an IP, use `/ip hotspot walled-garden ip add dst-address=<ip> action=accept`.

Note: HTTPS keeps working, but slowly, because of the 64 kbps limit. Only HTTP on port 80 is redirected to the isolation page. The address-list is sent by RADIUS as `Mikrotik-Address-List`, so the hotspot must authenticate through gobill's RADIUS.

## Custom HTML

In **Custom HTML** mode, the HTML you write is shown after its variables are replaced:

| Variable | Value |
|---|---|
| `[[company_name]]` | Company name |
| `[[logo_url]]` | Logo address |
| `[[title]]`, `[[message]]` | Isolation title and message |
| `[[username]]`, `[[name]]` | Customer username and name |
| `[[plan_name]]`, `[[expired_at]]`, `[[amount]]` | Last package, end time and price |
| `[[qris_image]]` | QRIS image (data URI), for an image `src` |
| `[[wa_admin_link]]` | Chat-admin link |
| `[[voucher_form]]` | Ready-made voucher form |
| `[[promos]]` | Promo blocks |
| `[[known]]` | `yes` when the customer is recognised, otherwise `no` |
| `[[notice]]` | Result message of a voucher attempt |

Values other than `voucher_form` and `promos` are HTML-escaped. Unknown or empty variables become empty. The HTML runs in a sandbox with no access to gobill's cookies, so scripts in it cannot read the admin session. Blank HTML falls back to the default page.

Use **Preview** to see the page with sample data. The preview uses the saved HTML, so save your changes first.

## Limits

- Isolation only applies to customers who log in through gobill's RADIUS.
- The customer is recognised from the framed IP of the most recent open RADIUS session. Make sure the NAS sends accounting correctly.
- The voucher form on the isolation page allows 10 wrong attempts per IP in 15 minutes.
