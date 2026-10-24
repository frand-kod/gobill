# Agent consignment vouchers

This document explains how to hand vouchers to an agent, record payments, and take back vouchers that were not sold.

**For:** operators (SuperAdmin, Admin), agents

**Prerequisites:** an admin account with the Agent role and at least one plan. See [configuration](configuration.md).

## How it works

You hand the agent a batch of vouchers first. The agent pays later. gobill keeps the agent's debt, payments, and returns in one append-only ledger per batch. The debt is always computed from that ledger and is never stored as a balance.

There is no agent deposit balance. The debt never goes below zero.

## Create a batch

Open **Agent batches > Add**. Fill in:

- **Agent**: an admin account with the Agent role.
- **Service Plan**: the plan of every voucher in the batch.
- **Paid vouchers**: how many vouchers the agent has to pay for.
- **Bonus vouchers**: how many free vouchers are given up front.
- **Price per paid voucher**: in rupiah.

gobill creates all the codes at once (paid plus bonus) and records a debt of paid vouchers times the price. This is one transaction. If it fails, no codes or debt are left behind.

Examples for the two profit-sharing models:

| Model | Paid | Bonus | Price | Debt |
|---|---|---|---|---|
| Bonus vouchers | 10 | 4 | Rp5,000 | Rp50,000 for 14 codes |
| Price difference | 20 | 0 | Rp5,000 | Rp100,000 for 20 codes |

For the price-difference model, set bonus to 0 and the price to the agent price. The agent's profit is the difference with their selling price, outside gobill.

The batch codes also show in the **Voucher** list as unused vouchers, so you can print them with **Print** there.

## Read a batch

Each batch shows:

- **Used**: codes a customer has redeemed. This is the least that was surely sold.
- **Unused**: codes that can still be redeemed.
- **Returned**: codes that were cancelled.
- **Debt**: what is left to pay.

gobill cannot know which vouchers the agent sold but no customer has redeemed yet. Payment is based on vouchers sold, so the used count is a lower bound. If the agent claims fewer sales than the used count, the claim is certainly too low.

An agent (Agent role) can see its own batches, usage, and debt under **Agent batches**. An agent cannot see another agent's batches, record payments, or return codes.

## Record a payment

Open the batch and choose **Record payment**. Enter the amount and a note. The debt drops by that amount. A payment above the debt is refused.

## Return codes

Open the batch, tick the unused codes, and choose **Return selected**.

- A returned code is cancelled. It cannot be redeemed any more, through the portal, the admin page, or RADIUS login.
- Each returned code lowers the debt by the price per voucher.
- Returns are capped at the number of paid vouchers in the batch. In a batch of 10 paid and 4 bonus, at most 10 codes can be returned.
- A code that has been used cannot be returned.

Payments and returns cannot be deleted or edited. The ledger is append-only on purpose.

## Limits

- Vouchers in a batch cannot be deleted from the voucher list.
- Payments are not in the income reports yet. The remaining debt is on the batch page.
