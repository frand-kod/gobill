-- Who may take a plan. public: sold on the customer portal. admin: only an admin gives it (Rp 0 staff plans).
ALTER TABLE plans ADD COLUMN audience TEXT NOT NULL DEFAULT 'public' CHECK (audience IN ('public', 'admin'));
