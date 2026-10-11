# Promotions

This document explains how to show short promos, for example a local shop, on the isolir page and on the customer portal dashboard.

**For:** operators (SuperAdmin)

**Prerequisites:** gobill is installed. See [installation](installation.md).

## Where to set them up

Open **Settings > Promotions**. Only SuperAdmin can see or change promos.

Each promo has:

- **Image** (optional): PNG, JPG, WebP, or ICO, 2 MB at most. The image is deleted from disk when the promo is deleted or its image is replaced.
- **Title** and **Text**: plain text. Line breaks are kept. HTML is shown as text.
- **Link** (optional): only `https://` or `http://`. It opens as a **Buka** (Open) button in a new tab, for example a WhatsApp link: `https://wa.me/628123456789`.
- **Where**: show on the isolir page (on by default), and/or on the customer portal dashboard (off by default).
- **Active**: off hides the promo everywhere.
- **Schedule** (optional): a start date and an end date. Empty means no limit.

Use the up and down arrows in the list to set the order. The first promo in the order is shown first.

## What customers see

- **Isolir page**: every active promo marked for it, as small cards in the order above. The image loads lazily to save bandwidth.
- **Portal dashboard**: one small card at the very bottom of the dashboard, with the first promo. **Lainnya** (More) shows the other promos. The **×** button hides the card on that device. Changing a promo (or adding one) shows the card again. The card does not move the plan or status cards.

Without JavaScript the card stays visible with the first promo only.

## Good practice

- Keep the promos few and short. Customers with an active plan should not be interrupted.
- Turn on the dashboard card only for offers customers care about, such as a discount.
- Use the schedule to end a promo on time.
