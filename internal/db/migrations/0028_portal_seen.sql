-- Which portal bell items a customer account has seen. version is a hash of the item's content, so an
-- item turns unread again when its content changes.
CREATE TABLE portal_seen (
    customer_id INTEGER NOT NULL REFERENCES customers (id) ON DELETE CASCADE,
    item        TEXT    NOT NULL,
    version     TEXT    NOT NULL,
    seen_at     INTEGER NOT NULL DEFAULT (unixepoch()),
    PRIMARY KEY (customer_id, item)
) WITHOUT ROWID;
