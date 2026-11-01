-- "Who had IP X at time T" looks sessions up by Framed-IP.
CREATE INDEX radius_sessions_ip_idx ON radius_sessions (framed_ip, started_at);
