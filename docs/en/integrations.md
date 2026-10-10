# Integrations

This document explains how to connect NuxBill to WhatsApp, SMS, Telegram, email, webhooks, and online payments. Online payments use Tripay or a static QRIS.

**For:** operators

**Prerequisites:** general settings are filled in, especially `app_url` for links in messages. See [configuration](configuration.md).

## Message switches

These switches are in **Settings > Notifications**, at the top, under "Global switches".

| Setting | Purpose |
|---|---|
| `notify_customers` | `no` stops all messages to customers. This includes reminders, expiry notices, invoices and QRIS links, welcome messages, balance messages, and manual messages. Summaries and operator notifications keep running. |
| `notify_otp` | `no` turns off OTP codes for registration, forgotten password, and contact change. The feature shows that the code is not available. |
| `expired_notify_minutes_before` | Sends the expiry message N minutes before the plan ends, in the range 0 to 1440. Default 0, which means when the plan ends. There is still one message per period. The plan still ends on time. A new extension or recharge sends a message again in its new period. |

`notify_customers` and `notify_otp` default to `yes` if never set.

Tip: after importing from PHPNuxBill for a parallel test, set `notify_customers` = `no`. This keeps customers from receiving duplicate messages. See [migration](migration-phpnuxbill.md#parallel-run-checklist).

## Message templates and channels

Message templates are in **Settings > Notifications**. Settings `notif_*` contain the templates for expiry, reminder, invoice, welcome, and balance messages. Settings `user_notification_*` set the channel for each message type.

Templates can use placeholders such as `[[price]]`. Unknown placeholders are removed from the message.

## WhatsApp

There are two ways to send WhatsApp messages. NuxBill chooses the one to use automatically.

1. **GOWA (recommended).** Use the [go-whatsapp-web-multidevice](https://github.com/aldinokemal/go-whatsapp-web-multidevice) server.
2. **Message gateway `wa_url`.** Used only when `alt_wga_server_url` is empty.

### Option 1: GOWA

Fill in **Settings > Integrations**, section "WhatsApp — GOWA":

| Setting | Value |
|---|---|
| `alt_wga_server_url` | Address of the GOWA server, for example `http://127.0.0.1:3030`. |
| `alt_wga_device_id` | Optional. |
| `alt_wga_username` and `alt_wga_password` | Basic auth, if the server uses it. |

NuxBill sends `POST <alt_wga_server_url>/send/message` with the body `{"phone":"628xxx@s.whatsapp.net","message":"..."}`. This is the same method as the "Alternative WhatsApp Gateway" plugin in PHPNuxBill. Numbers that start with `0` are converted using `country_code_phone`.

### Option 2: message gateway `wa_url`

`wa_url` is a URL template with the placeholders `[number]` and `[text]`. The same setting is used for SMS. Other WhatsApp gateways, such as Fonnte, Wablas, and WAHA, have no dedicated integration yet. For now, use `wa_url` if the gateway supports GET with `[number]` and `[text]`.

### Priority rules

- If `alt_wga_server_url` is set, `wa_url` is ignored completely.
- If `wa_url` still contains the address of the old PHP plugin, that is, `...?_route=plugin/wga_sendMessage&...`, NuxBill writes a warning to the log. NuxBill still sends directly to the WA server. Clear `wa_url` to avoid confusion.

The "Send test message" button on the same page sends one message to a number you type. It uses the values in the form, even if they are not saved yet. The server's reply is shown in plain language.

WhatsApp devices and QR login are not managed in NuxBill. Do those on the WA server's own page.

## SMS

SMS uses `wa_url` as the gateway. The setting `sms_url` is read only for compatibility with PHPNuxBill imports. If `sms_url` has a value, SMS uses `sms_url`. In **Settings > Integrations**, its value appears in the gateway column and is moved to `wa_url` when saved.

## Telegram

| Setting | Purpose |
|---|---|
| `telegram_bot` | Telegram bot token. |
| `telegram_target_id` | Telegram destination ID, for messages and operator alerts. |

Operator alerts use the channel set in [monitoring](monitoring.md#alert-settings).

## Email (SMTP)

| Setting | Purpose |
|---|---|
| `smtp_host`, `smtp_port` | SMTP server and its port. |
| `smtp_user`, `smtp_pass` | SMTP account. |
| `smtp_ssltls` | SMTP TLS mode. |
| `mail_from`, `mail_reply_to` | Sender address and reply-to address. |

## Webhook

| Setting | Purpose |
|---|---|
| `webhook_url` | URL that receives outgoing events. |
| `webhook_secret` | Secret used for the signature. The signature is sent in the `X-Signature` header. |

## Connection tests

Each section has a test button in **Settings > Integrations**. Only SuperAdmin can run tests, and the limit is 5 tests per minute. Save first, because tests use the saved values. Tests are not affected by `notify_customers`, because they are meant for the operator.

| Button | What happens |
|---|---|
| Send Telegram test message | Sends a short message to `telegram_target_id`. |
| Send gateway test | Sends an SMS to a number you type, through the `wa_url` gateway. |
| Send email test | Sends an email to an address you type, through the saved SMTP settings. |
| Send webhook test | Sends a signed `test` event to `webhook_url`, and shows the HTTP status. |
| Check connection | In **Settings > Payment Gateway**, for Tripay. Calls the Tripay payment channel list to confirm that the key and merchant code are correct. |

## Online payments

The setting `payment_gateway` selects the gateway: `""` for off, or `tripay`. The settings are in **Settings > Payment Gateway**.

| Setting | Purpose |
|---|---|
| `payment_gateway` | `""` for off, or `tripay`. |
| `tripay_mode`, `tripay_merchant_code`, `tripay_api_key`, `tripay_private_key`, `tripay_channel` | Tripay gateway settings. The API key and private key are stored as plain text in the `settings` table. See [security](security.md#application-security). |
| `qris_payload` | Text of the merchant's static QRIS. It is filled in by uploading a QRIS photo. The image itself is not stored. |

Tripay callbacks go to `POST /callback/tripay`. The callback is verified with its signature.

## Static QRIS

In **Settings > Payment Gateway**, in the QRIS section:

1. Upload a photo of the merchant's static QRIS. PNG or JPG, maximum 2 MB.
2. The system reads the QR code and checks that it is a valid QRIS.
3. The system stores only the text, in `qris_payload`. The image is not stored.
4. The active merchant name and NMID appear below the field.
5. The "Remove QRIS" button clears the setting.

QRIS text can also be pasted in "Advanced options".

For each invoice, the system creates a QR code locked to the amount. The link is sent by WhatsApp, in the placeholder `[[qris_link]]`. If the template does not use that placeholder, the link is added at the end of the message. The link requires `app_url`. The customer does not need to log in.

Notes:

- The system does not verify QRIS payments. Payment confirmation is still done manually.
- Recharges already paid with balance, a gateway, or a voucher do not get this link.

## See also

- [Configuration](configuration.md): general and business settings.
- [Monitoring](monitoring.md): operator alerts and daily summary.
- [Security](security.md): secret storage and callback verification.
- [Migrating from PHPNuxBill](migration-phpnuxbill.md#whatsapp-after-cutover): WhatsApp settings after the move.
