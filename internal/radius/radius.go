// Package radius is the built-in RADIUS auth (rlm_rest replacement) and accounting server.
// Behaviour follows phpnuxbill radius.php.
package radius

// StaleAfter is how long (seconds) an open session may go without an update before it no longer
// counts toward shared_users and shows as stale. The default is 2x a 5-minute interim update;
// GOBILL_STALE_AFTER overrides it at startup (see cmd/gobill). It is the floor: StaleFor widens it
// per NAS to the observed interim interval.
var StaleAfter int64 = 600
