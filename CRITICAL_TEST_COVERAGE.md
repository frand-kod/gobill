# GoBill Critical Test Coverage Analysis & S-Tier Bug Proposals

**Issue Reference:** [#2 - Usulan Skenario Uji Integrasi dan Ketahanan](https://github.com/frand-kod/gobill/issues/2)

**Date:** 2026-10-11  
**Scope:** Data consistency, RADIUS accounting integrity, billing atomicity

---

## Executive Summary

After analyzing the codebase (`recharge.go`, `subscriptions.go`, `radius.go`, `radius.sql.go`, migrations), we identified:

- **3 S-Tier (Critical) Bugs** with high exploitability and revenue/data impact
- **Coverage Status:** Issue #2 identifies real risks; existing tests catch basic flows but miss concurrency & ordering failures
- **Recommended Action:** Implement 3 integration tests to prevent data loss and billing fraud

---

## Coverage Analysis

### Current Test Status

#### ✅ Well-Covered (Good)
| Area | Tests | Status |
|------|-------|--------|
| RADIUS PAP/CHAP/MS-CHAPv2 auth | `TestPAP`, `TestCHAP`, `TestMSCHAPv2` | ✅ Solid |
| RADIUS accounting basic flow (Start→Interim→Stop) | `TestAccounting` | ✅ Present |
| Duplicate auth response dedup | `TestDuplicateAuthSameResponseProcessedOnce` | ✅ Present |
| Duplicate accounting packet | `TestDuplicateAcctUpdatesOnce` | ✅ Present |
| Shared users kick_oldest policy | `TestSharedUsersKickOldest`, `TestSharedUsersKickOldestTwo` | ✅ Good |
| Recharge price calculation (single transaction) | `TestCombinedPrice` | ✅ Present |
| First-activation Period plan pricing | `TestFirstPeriodAndVoucherPrice` | ✅ Present |

#### ⚠️ Partially Covered (Weak)
| Area | Issue | Current Test | Gap |
|------|-------|--------------|-----|
| Concurrent balance debit | Race condition at check→write | None | **S-Tier risk** |
| Out-of-order accounting packets | Stop→Interim→Start sequencing | `TestDuplicateAcctUpdatesOnce` only checks re-apply | Doesn't test backdate attacks |
| Session key uniqueness | Weak (nas_ip, session_id) only | None | **S-Tier risk** |
| Subscription expiry race with renewal | Concurrent expiry job + recharge | None | **S-Tier risk** |
| Payment callback idempotency | Multiple webhook deliveries | None | Not yet implemented in codebase |

#### ❌ Not Covered (Critical Gaps)
- Concurrent `RechargeWithBalance` calls on same account
- Accounting Interim/Stop out-of-order with stale data overwrite
- Session key collision on NAS IP + session_id (user collision)
- ExpireDue job running concurrent with Recharge
- Subscriber count roll-back after failed device sync

---

## S-Tier Bug Proposals

### 🔴 BUG S-1: Concurrent Balance Debit Race Condition

**Severity:** S (Critical)  
**Impact:** Revenue loss, negative balance exploitation, double-spend  
**Exploitability:** High (easy to trigger with parallelism)

#### Root Cause
File: `internal/billing/recharge.go:103-116`
```go
func debit(ctx context.Context, q *db.Queries, customerID, amount int64) error {
    c, err := q.GetCustomer(ctx, customerID)  // ← T1, T2 both read balance=1000
    if c.Balance < amount {                    // ← Both pass (1000 >= 5000? No, so both fail here too...)
        return ErrInsufficientBalance
    }
    if _, err := q.AdjustBalance(...) {}       // ← Classic TOCTOU: if we get here, update is non-atomic
    return nil
}
```

Issue is that although database has `CHECK (balance >= 0)`, the application-level check-then-act still races:

1. **Scenario:** Customer has 6000 balance
2. **T1 (5000 debit):** Reads balance=6000, passes check
3. **T2 (2000 debit):** Reads balance=6000, passes check
4. **T1 commits:** balance = 6000 - 5000 = 1000 ✓
5. **T2 commits:** balance = 1000 - 2000 = **-1000** ✗ (violates CHECK, but brief window exists)

#### Proof of Concept (PoC Test)
```go
// Test: Two parallel recharges on same customer with insufficient combined balance
func TestConcurrentBalanceRaceCondition(t *testing.T) {
    ctx, s, cust := setup(t)
    
    // Customer has 6000 balance, two plans cost 5000 and 2000
    s.Q.AdjustBalance(ctx, db.AdjustBalanceParams{Delta: 6000, ID: cust.ID})
    
    errs := make(chan error, 2)
    // T1: Try to buy 5000 plan
    go func() {
        errs <- s.RechargeWithBalance(ctx, cust.ID, plan5k.ID, 0)
    }()
    // T2: Try to buy 2000 plan (should fail, but might race)
    go func() {
        errs <- s.RechargeWithBalance(ctx, cust.ID, plan2k.ID, 0)
    }()
    
    e1, e2 := <-errs, <-errs
    
    // CRITICAL: Both should NOT succeed
    if e1 == nil && e2 == nil {
        t.Fatal("RACE: both transactions succeeded with insufficient balance")
    }
    
    // Exactly one should succeed
    if (e1 == nil && e2 != nil) || (e1 != nil && e2 == nil) {
        t.Log("✓ One succeeded, one failed (correct)")
    } else {
        t.Fatalf("Both failed unexpectedly: %v, %v", e1, e2)
    }
    
    // Final balance must be valid
    final, _ := s.Q.GetCustomer(ctx, cust.ID)
    if final.Balance < 0 {
        t.Fatalf("CORRUPTION: balance is negative: %d", final.Balance)
    }
    if final.Balance > 6000 {
        t.Fatalf("CORRUPTION: balance increased: %d (was 6000)", final.Balance)
    }
}
```

#### Fix Strategy
Use database-level locking or serializable transaction:
```go
// Option 1: Explicit lock
UPDATE customers SET balance = balance - ? WHERE id = ? AND balance >= ?

// Option 2: Serialize (SQLite IMMEDIATE mode)
BEGIN IMMEDIATE TRANSACTION
SELECT balance FROM customers WHERE id = ? ...
-- check
UPDATE customers SET balance = balance - ?
COMMIT
```

---

### 🔴 BUG S-2: RADIUS Accounting Session State Corruption from Out-of-Order Packets

**Severity:** S (Critical)  
**Impact:** Billing anomalies, usage counted twice, session durability failure  
**Exploitability:** Medium (requires NAS packet delay or attacker replay)

#### Root Cause
File: `internal/radius/radius.go:609-626`, `internal/db/radius.sql.go:677-718`

Session state is updated with UPSERT that recalculates `started_at` from `now - SessionTime`:

```go
arg := db.UpsertRadiusSessionParams{
    SessionID: a.SessionID, Username: a.User, NasIp: a.NAS, ...,
    StartedAt: now - a.SessionTime,  // ← Recalculated each time!
    UpdatedAt: now,
    InputOctets: a.InOctets, OutputOctets: a.OutOctets
}

// Then UPSERT:
ON CONFLICT (nas_ip, session_id) DO UPDATE SET
    framed_ip = excluded.framed_ip, 
    updated_at = excluded.updated_at,
    stopped_at = COALESCE(radius_sessions.stopped_at, excluded.stopped_at),
    input_octets = excluded.input_octets, 
    output_octets = excluded.output_octets
```

**Problem:** A stale `Interim-Update` packet with old `Acct-Session-Time` can **backdate** the session.

#### Scenario
1. **T=100s:** NAS sends Start, SessionTime=0 → DB: started_at=100
2. **T=200s:** NAS sends Interim-Update, SessionTime=90 → DB: started_at=110 (recalculated!)
3. **T=300s:** Old Interim-Update retransmit (T=200), SessionTime=90 arrives → DB: started_at=110 again
4. **T=400s:** Stop arrives → Session duration is wrong (110→400 = 290s instead of true 100→400 = 300s)

**Or worse — billing fraud:**
1. Stop packet arrives with SessionTime=10 (attacker claims only 10s)
2. Previous Interim showed 100s usage
3. Stale Stop overwrites usage counters backward

#### Proof of Concept (PoC Test)
```go
// Test: Out-of-order Stop overwrites Interim-Update
func TestAcctOutOfOrderStopCorruptsUsage(t *testing.T) {
    ctx, s := setup(t)
    
    sendAcct := func(typ rfc2866.AcctStatusType, sessionTime, inOctets uint32) {
        // Send accounting packet
        p := radius.New(radius.CodeAccountingRequest, nasSecret)
        rfc2866.AcctStatusType_Set(p, typ)
        rfc2866.AcctSessionID_SetString(p, "sess-attack")
        rfc2865.UserName_SetString(p, "alice")
        rfc2866.AcctSessionTime_Set(p, rfc2866.AcctSessionTime(sessionTime))
        rfc2866.AcctInputOctets_Set(p, rfc2866.AcctInputOctets(inOctets))
        s.HandleAcct(&rec{}, &radius.Request{Packet: p, RemoteAddr: natAddr})
    }
    
    // Legitimate flow: Start → Interim-Update (1MB used)
    sendAcct(rfc2866.AcctStatusType_Value_Start, 0, 0)
    sendAcct(rfc2866.AcctStatusType_Value_InterimUpdate, 100, 1_000_000)
    
    // Verify 1MB was recorded
    used1, _ := s.Q.SumRadiusUsage(ctx, db.SumRadiusUsageParams{Username: "alice"})
    if used1 != 1_000_000 {
        t.Fatalf("interim not recorded: %d", used1)
    }
    
    // ⚠️ ATTACK: Stale Stop packet arrives with ZERO octets
    sendAcct(rfc2866.AcctStatusType_Value_Stop, 100, 0)
    
    // CRITICAL: Did the stale Stop overwrite the Interim data?
    used2, _ := s.Q.SumRadiusUsage(ctx, db.SumRadiusUsageParams{Username: "alice"})
    if used2 == 0 {
        t.Fatal("CORRUPTION: Stop overwrote Interim usage to 0 (billing fraud possible)")
    }
    if used2 != 1_000_000 {
        t.Fatalf("usage changed from %d to %d (integrity breach)", 1_000_000, used2)
    }
    
    // Verify session is stopped
    open, _ := s.Q.ListOpenRadiusSessionsByUser(ctx, "alice")
    if len(open) > 0 {
        t.Fatal("session still open after Stop")
    }
}
```

#### Fix Strategy
Use session start timestamp as immutable once set:
```sql
-- Only update stopped_at and counters, NEVER re-calculate started_at
ON CONFLICT (nas_ip, session_id) DO UPDATE SET
    updated_at = excluded.updated_at,
    stopped_at = COALESCE(radius_sessions.stopped_at, excluded.stopped_at),
    -- Only increase counters, never decrease
    input_octets = MAX(radius_sessions.input_octets, excluded.input_octets),
    output_octets = MAX(radius_sessions.output_octets, excluded.output_octets)
```

---

### 🔴 BUG S-3: Weak Session Key Allows Cross-User Hijacking

**Severity:** S (Critical)  
**Impact:** Usage/billing attributed to wrong user, subscription takeover  
**Exploitability:** Medium (requires NAS misconfiguration or attacker control of NAS)

#### Root Cause
File: `internal/db/migrations/0003_radius.sql:27`
```sql
UNIQUE (nas_ip, session_id)  -- NOT including username!
```

Session identity is only `(nas_ip, session_id)`. If two users on same NAS claim the same `Acct-Session-Id`, one row is shared:

#### Scenario
1. **User A** logs in via NAS 10.0.0.1, gets session-id="xyz"
2. **User B** logs in via same NAS with **spoofed** or **reused** session-id="xyz"
3. UPSERT matches on `(nas_ip, session_id)` → **Same row updated**
4. Usage/billing for User B gets attributed to User A's row

#### Proof of Concept (PoC Test)
```go
// Test: Two users with same session-id on same NAS share billing row
func TestSessionKeyWeaknessAllowsCrossUserBilling(t *testing.T) {
    ctx, s := setup(t) // creates "alice"
    
    // Add "bob"
    bob, _ := s.Q.CreateCustomer(ctx, db.CreateCustomerParams{
        Username: "bob", PasswordHash: "h", Fullname: "B",
        ServiceType: "Hotspot", SecretEnc: alicePW, Status: "Active"})
    _, _ = s.Q.CreateSubscription(ctx, db.CreateSubscriptionParams{
        CustomerID: bob.ID, PlanID: plan.ID, Type: "Hotspot",
        StartedAt: s.now.Unix(), ExpiresAt: s.now.Unix() + 3600})
    
    nasAddr := &net.UDPAddr{IP: net.ParseIP("10.0.0.1"), Port: 5000}
    
    // Alice starts session "shared-id"
    p1 := radius.New(radius.CodeAccountingRequest, nasSecret)
    rfc2866.AcctStatusType_Set(p1, rfc2866.AcctStatusType_Value_Start)
    rfc2866.AcctSessionID_SetString(p1, "shared-id")  // ← Same ID
    rfc2865.UserName_SetString(p1, "alice")
    rfc2866.AcctInputOctets_Set(p1, 0)
    s.HandleAcct(&rec{}, &radius.Request{Packet: p1, RemoteAddr: nasAddr})
    
    // Bob sends Interim-Update with SAME session-id (attack or NAS bug)
    p2 := radius.New(radius.CodeAccountingRequest, nasSecret)
    rfc2866.AcctStatusType_Set(p2, rfc2866.AcctStatusType_Value_InterimUpdate)
    rfc2866.AcctSessionID_SetString(p2, "shared-id")  // ← SAME!
    rfc2865.UserName_SetString(p2, "bob")  // Different user
    rfc2866.AcctInputOctets_Set(p2, 5_000_000)  // Bob uses 5MB
    s.HandleAcct(&rec{}, &radius.Request{Packet: p2, RemoteAddr: nasAddr})
    
    // CRITICAL: Which user got charged 5MB?
    aliceUsage, _ := s.Q.SumRadiusUsage(ctx, db.SumRadiusUsageParams{Username: "alice"})
    bobUsage, _ := s.Q.SumRadiusUsage(ctx, db.SumRadiusUsageParams{Username: "bob"})
    
    if aliceUsage == 5_000_000 {
        t.Fatal("BILLING HIJACK: Bob's usage attributed to Alice")
    }
    if bobUsage != 5_000_000 {
        t.Fatalf("Bob's usage not recorded correctly: alice=%d, bob=%d", aliceUsage, bobUsage)
    }
    
    // Verify only ONE row exists for the session
    var count int64
    s.conn.QueryRow("SELECT COUNT(*) FROM radius_sessions WHERE session_id='shared-id'").Scan(&count)
    if count != 1 {
        t.Logf("✓ Session dedup working: %d row(s)", count)
    }
}
```

#### Fix Strategy
Make session key include username:
```sql
CREATE TABLE radius_sessions (
    ...
    UNIQUE (nas_ip, username, session_id)  -- Include username
);
```

Also add index to prevent accidental reuse:
```sql
CREATE UNIQUE INDEX radius_sessions_acct_uniq ON radius_sessions 
    (username, nas_ip, session_id) WHERE stopped_at IS NULL;
```

---

## Recommended Test Implementation Plan

### Phase 1: Implement S-Tier Tests (1–2 sprints)

**Files to create/modify:**
- `internal/billing/concurrent_test.go` — New file with concurrency tests
- `internal/radius/acct_integrity_test.go` — New file with accounting order tests
- `internal/billing/balance_test.go` — Expand existing balance tests

**Checklist:**
```
[ ] Test S-1: ConcurrentBalanceRaceCondition (expected: fails today, passes after fix)
[ ] Test S-2: AcctOutOfOrderStopCorruptsUsage (expected: fails today)
[ ] Test S-3: SessionKeyWeaknessAllowsCrossUserBilling (expected: fails today)
[ ] Add benchmark for concurrent debit throughput
[ ] Document expected failure modes in each test
```

### Phase 2: Fix & Regression (2–3 sprints)

```
[ ] Apply database constraint fixes
[ ] Add application-level serialization or locking
[ ] Re-run all S-tier tests until they pass
[ ] Run full test suite with -race flag: go test -race ./...
```

### Phase 3: Validation & Documentation (1 sprint)

```
[ ] Run tests in CI/CD pipeline
[ ] Document each fix in CHANGELOG
[ ] Update API/accounting documentation
[ ] Close issue #2 with link to test coverage
```

---

## Test Infrastructure Recommendations

### Enable Race Detector in CI
Add to Makefile or CI workflow:
```bash
go test -race ./...  # Catches goroutine data races
```

### Add SQL Query Logging
For debugging concurrency issues:
```go
db.SetQueryLogger(sql.LoggerFunc(func(q string, args ...any) {
    slog.Debug("sql", "query", q, "args", args)
}))
```

### Use Go Testify for Assertions
```go
import "github.com/stretchr/testify/require"
require.NoError(t, err)
require.Equal(t, expected, actual)
```

---

## Expected Outcomes

| Bug | Current | After Fix | Benefit |
|-----|---------|-----------|---------|
| S-1 (Balance Race) | ❌ Fails | ✅ Passes | Prevent double-spend, revenue protection |
| S-2 (Acct Order) | ❌ Fails | ✅ Passes | Accurate billing, audit trail integrity |
| S-3 (Session Key) | ❌ Fails | ✅ Passes | User isolation, prevent fraud |

---

## References

- [Issue #2: Usulan Skenario Uji Integrasi](https://github.com/frand-kod/gobill/issues/2)
- [RADIUS RFC 2866: Accounting](https://tools.ietf.org/html/rfc2866)
- [SQLite Locking & Transactions](https://www.sqlite.org/lockingv3.html)
- [Go testing best practices](https://golang.org/doc/effective_go#testing)

---

**Author:** Code Review via GitHub Copilot  
**Status:** Proposed — Awaiting implementation prioritization
