package web

// One-time codes: generation, sending and rate limits.

import (
	"crypto/rand"
	"math/big"

	"context"
	"errors"
	"github.com/frand-kod/gobill/internal/notify"
	"strconv"
	"time"
)

// newOTP returns a 6-digit code from crypto/rand.
func newOTP() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(900000))
	if err != nil {
		return "", err
	}
	return strconv.FormatInt(n.Int64()+100000, 10), nil
}

// otpAllow records and permits one OTP send: per IP max 5 per 15 min; per phone a 60s cooldown
// and max 5 per hour. An empty phone only counts against the IP.
// ponytail: in-memory, resets on restart; persist if abuse needs to survive restarts.
func (s *Server) otpAllow(ip, phone string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.otpSent == nil {
		s.otpSent = map[string][]time.Time{}
	}
	now := time.Now()
	recent := func(k string, win time.Duration) []time.Time {
		var l []time.Time
		for _, t := range s.otpSent[k] {
			if now.Sub(t) < win {
				l = append(l, t)
			}
		}
		return l
	}
	if len(s.otpSent) > 10000 { // bound memory: drop keys with no send in the last hour
		for k := range s.otpSent {
			if len(recent(k, time.Hour)) == 0 {
				delete(s.otpSent, k)
			}
		}
	}
	ik, pk := "ip:"+ip, "ph:"+phone
	il, pl := recent(ik, 15*time.Minute), recent(pk, time.Hour)
	if len(il) >= 5 || phone != "" && (len(pl) >= 5 || len(pl) > 0 && now.Sub(pl[len(pl)-1]) < time.Minute) {
		return false
	}
	s.otpSent[ik] = append(il, now)
	if phone != "" {
		s.otpSent[pk] = append(pl, now)
	}
	return true
}

// otpOff is the notify_otp switch: "no" means no verification code is sent, so OTP flows cannot complete.
func otpOff(st map[string]string) bool { return st["notify_otp"] == "no" }

// otpEnabled: old sms_otp_registration, but only when a WA/SMS gateway exists to deliver it.
func otpEnabled(st map[string]string) bool {
	return st["sms_otp_registration"] == "yes" && (notify.SMSConfigured(st) || notify.WAConfigured(st))
}

func (s *Server) sendOTP(ctx context.Context, st map[string]string, phone, label, otp string) error {
	n, err := notify.Load(ctx, s.queries)
	if err != nil {
		return err
	}
	msg := st["company_name"] + "\n\n" + s.catalog.T(s.language(), label) + "\n" + otp
	return deliverOTP(ctx, n, st["phone_otp_type"], phone, msg)
}

// deliverOTP sends msg on the channels typ names. "both" tries WhatsApp and SMS and succeeds when
// either works; the error joins both failures.
func deliverOTP(ctx context.Context, n *notify.Notifier, typ, phone, msg string) error {
	var waErr, smsErr error
	if typ == "whatsapp" || typ == "both" {
		waErr = n.WhatsApp(ctx, phone, msg)
	}
	if typ != "whatsapp" {
		smsErr = n.SMS(ctx, phone, msg)
	}
	if typ == "both" && (waErr == nil || smsErr == nil) {
		return nil
	}
	return errors.Join(waErr, smsErr)
}
