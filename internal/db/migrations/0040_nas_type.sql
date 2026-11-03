-- Which speed and quota attributes the NAS understands. mikrotik: Mikrotik-Rate-Limit and
-- Mikrotik-Total-Limit (as before). wispr: WISPr-Bandwidth-Max-Up/Down and CoovaChilli-Max-Total-Octets
-- (CoovaChilli on OpenWrt, controllers that speak WISPr). standard: no vendor attributes at all.
ALTER TABLE nas ADD COLUMN type TEXT NOT NULL DEFAULT 'mikrotik' CHECK (type IN ('mikrotik', 'wispr', 'standard'));
