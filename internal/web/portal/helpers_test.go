package portal

import "testing"

func TestMaskMAC(t *testing.T) {
	for in, want := range map[string]string{
		"CE:33:AA:BB:2A:AA": "CE:33:••:••:2A:AA",
		"ce-33-aa-bb-2a-aa": "ce:33:••:••:2a:aa",
		"":                  "",
		"garbage":           "",
	} {
		if got := maskMAC(in); got != want {
			t.Errorf("maskMAC(%q) = %q, want %q", in, got, want)
		}
	}
}

// The lookup answers any signed-in customer, so it gives a masked name only.
func TestMaskName(t *testing.T) {
	for in, want := range map[string]string{"Budi Santoso": "Budi S.", "Siti Nur Aisyah": "Siti N. A.", "Two": "Two", "": ""} {
		if got := maskName(in); got != want {
			t.Errorf("maskName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestOTPWait(t *testing.T) {
	if otpWait(1000, 1010) != 50 || otpWait(1000, 1060) != 0 || otpWait(1000, 2000) != 0 || otpWait(0, 1000) != 0 {
		t.Fatal("resend gap is not 60 seconds")
	}
}
