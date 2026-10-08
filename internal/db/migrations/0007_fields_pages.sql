-- F5 customer custom fields and static pages.

-- Admin-defined customer fields. options: comma-separated, used by type 'select' only.
CREATE TABLE custom_fields (
    id         INTEGER PRIMARY KEY,
    name       TEXT    NOT NULL UNIQUE,
    type       TEXT    NOT NULL CHECK (type IN ('text', 'number', 'select', 'date')),
    options    TEXT    NOT NULL DEFAULT '',
    required   INTEGER NOT NULL DEFAULT 0,
    sort_order INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE customer_field_values (
    customer_id INTEGER NOT NULL REFERENCES customers (id) ON DELETE CASCADE,
    field_id    INTEGER NOT NULL REFERENCES custom_fields (id) ON DELETE CASCADE,
    value       TEXT    NOT NULL DEFAULT '',
    PRIMARY KEY (customer_id, field_id)
) WITHOUT ROWID;

-- Static pages, plain text shown escaped with line breaks.
CREATE TABLE pages (
    slug  TEXT PRIMARY KEY,
    title TEXT NOT NULL,
    body  TEXT NOT NULL DEFAULT ''
) WITHOUT ROWID;

INSERT INTO pages (slug, title) VALUES
    ('announcement', 'Announcement'),
    ('tos', 'Terms and Conditions'),
    ('privacy', 'Privacy Policy'),
    ('registration', 'Registration Info');
