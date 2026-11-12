package core

// One-time codes: generation, sending and rate limits.

import (
	"context"
	"crypto/rand"
	"errors"
	"math/big"
	"strconv"
	"time"

	"github.com/frand-kod/gobill/internal/notify"
)

// NewOTP returns a 6-digit code from crypto/rand.
func NewOTP() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(900000))
	if err != nil {
		return "", err
	}
	return strconv.FormatInt(n.Int64()+100000, 10), nil
}

// OtpAllow records and permits one OTP send: per IP max 5 per 15 min; per phone a 60s cooldown
// and max 5 per hour. An empty phone only counts against the IP.
// ponytail: in-memory, resets on restart; persist if abuse needs to survive restarts.
func (s *Core) OtpAllow(ip, phone string) bool {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	if s.OtpSent == nil {
		s.OtpSent = map[string][]time.Time{}
	}
	now := time.Now()
	recent := func(k string, win time.Duration) []time.Time {
		var l []time.Time
		for _, t := range s.OtpSent[k] {
			if now.Sub(t) < win {
				l = append(l, t)
			}
		}
		return l
	}
	if len(s.OtpSent) > 10000 { // bound memory: drop keys with no send in the last hour
		for k := range s.OtpSent {
			if len(recent(k, time.Hour)) == 0 {
				delete(s.OtpSent, k)
			}
		}
	}
	ik, pk := "ip:"+ip, "ph:"+phone
	il, pl := recent(ik, 15*time.Minute), recent(pk, time.Hour)
	if len(il) >= 5 || phone != "" && (len(pl) >= 5 || len(pl) > 0 && now.Sub(pl[len(pl)-1]) < time.Minute) {
		return false
	}
	s.OtpSent[ik] = append(il, now)
	if phone != "" {
		s.OtpSent[pk] = append(pl, now)
	}
	return true
}

// OtpOff is the notify_otp switch: "no" means no verification code is sent, so OTP flows cannot complete.
func OtpOff(st map[string]string) bool { return st["notify_otp"] == "no" }

// OtpEnabled: old sms_otp_registration, but only when a WA/SMS gateway exists to deliver it.
func OtpEnabled(st map[string]string) bool {
	return st["sms_otp_registration"] == "yes" && (notify.SMSConfigured(st) || notify.WAConfigured(st))
}

func (s *Core) SendOTP(ctx context.Context, st map[string]string, phone, label, otp string) error {
	n, err := notify.Load(ctx, s.Queries)
	if err != nil {
		return err
	}
	msg := st["company_name"] + "\n\n" + s.T(label) + "\n" + otp
	return DeliverOTP(ctx, n, st["phone_otp_type"], phone, msg)
}

// DeliverOTP sends msg on the channels typ names. "both" tries WhatsApp and SMS and succeeds when
// either works; the error joins both failures.
func DeliverOTP(ctx context.Context, n *notify.Notifier, typ, phone, msg string) error {
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
