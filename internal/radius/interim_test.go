package radius

import (
	"testing"

	"layeh.com/radius/rfc2866"
)

func TestStaleForFollowsInterim(t *testing.T) {
	const start, upd, stop = rfc2866.AcctStatusType_Value_Start, rfc2866.AcctStatusType_Value_InterimUpdate, rfc2866.AcctStatusType_Value_Stop
	nas := "10.9.9.9"
	t.Cleanup(func() { delete(interim.every, nas) })
	if got := StaleFor(nas); got != StaleAfter {
		t.Fatalf("unknown NAS: %d, want the floor %d", got, StaleAfter)
	}
	noteInterim(nas, "a", start, 1000)
	noteInterim(nas, "a", upd, 1003) // retransmit-like, ignored
	noteInterim(nas, "a", upd, 1903) // 15-minute interim
	if got := StaleFor(nas); got != 2*900+60 {
		t.Fatalf("StaleFor %d, want %d", got, 2*900+60)
	}
	if StaleMax() < 2*900+60 {
		t.Fatal("StaleMax below the slowest NAS")
	}
	noteInterim(nas, "a", stop, 2000)
	if _, ok := interim.last[nas+"\x00a"]; ok {
		t.Fatal("Stop left the session in the map")
	}
	noteInterim(nas, "b", start, 3000)
	noteInterim(nas, "b", upd, 3060) // 1-minute interim: below the floor
	if got := StaleFor(nas); got != StaleAfter {
		t.Fatalf("short interval: %d, want the floor %d", got, StaleAfter)
	}
}
