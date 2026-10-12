package payment

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// QRIS is an EMVCo TLV string: tag (2), length (2 decimal), value. Static QRIS has no amount;
// QRISAmount sets tag 54 and a new CRC (tag 63) so the scanner fills in the price.

type tlv struct{ tag, val string }

func qrisParse(s string) ([]tlv, error) {
	var out []tlv
	for i := 0; i < len(s); {
		if i+4 > len(s) {
			return nil, errors.New("QRIS: truncated TLV")
		}
		n, err := strconv.Atoi(s[i+2 : i+4])
		if err != nil || n < 0 || i+4+n > len(s) {
			return nil, errors.New("QRIS: bad TLV length")
		}
		out = append(out, tlv{s[i : i+2], s[i+4 : i+4+n]})
		i += 4 + n
	}
	return out, nil
}

// crc16 is CRC-16/CCITT-FALSE, the checksum EMVCo uses.
func crc16(s string) uint16 {
	crc := uint16(0xFFFF)
	for i := 0; i < len(s); i++ {
		crc ^= uint16(s[i]) << 8
		for range 8 {
			if crc&0x8000 != 0 {
				crc = crc<<1 ^ 0x1021
			} else {
				crc <<= 1
			}
		}
	}
	return crc
}

// QRISValid checks the header, the TLV structure and the CRC of a QRIS payload.
func QRISValid(s string) error {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "000201") {
		return errors.New("QRIS: must start with 000201")
	}
	if len(s) < 8 || s[len(s)-8:len(s)-4] != "6304" {
		return errors.New("QRIS: CRC tag 6304 missing")
	}
	if _, err := qrisParse(s); err != nil {
		return err
	}
	want, err := strconv.ParseUint(s[len(s)-4:], 16, 16)
	if err != nil || uint16(want) != crc16(s[:len(s)-4]) {
		return errors.New("QRIS: CRC mismatch")
	}
	return nil
}

// QRISAmount returns the static payload locked to amount (rupiah), with a fresh CRC.
func QRISAmount(static string, amount int64) (string, error) {
	if amount <= 0 {
		return "", errors.New("QRIS: amount must be above 0")
	}
	if err := QRISValid(static); err != nil {
		return "", err
	}
	tlvs, err := qrisParse(strings.TrimSpace(static))
	if err != nil {
		return "", err
	}
	var b strings.Builder
	put := func(t tlv) { fmt.Fprintf(&b, "%s%02d%s", t.tag, len(t.val), t.val) }
	amt := strconv.FormatInt(amount, 10)
	placed := false
	for _, t := range tlvs {
		switch t.tag {
		case "63", "54":
			continue
		case "01":
			t.val = "12" // dynamic: one payment per code
		case "58":
			put(tlv{"54", amt})
			placed = true
		}
		put(t)
	}
	if !placed {
		put(tlv{"54", amt})
	}
	b.WriteString("6304")
	fmt.Fprintf(&b, "%04X", crc16(b.String()))
	return b.String(), nil
}

// QRISDetails are the parts of a QRIS a customer checks before paying.
type QRISDetails struct {
	MerchantName string // tag 59
	City         string // tag 60
	NMID         string // tag 51, sub-tag 02
	Provider     string // from the merchant account domain in tags 26-45, "" when unknown
}

// providers maps the start of a merchant account domain to the e-wallet or bank name shown to
// customers. Other domains are shown as they are.
var providers = []struct{ prefix, name string }{
	{"COM.GO-JEK", "GoPay"}, {"ID.DANA", "DANA"}, {"ID.CO.SHOPEE", "ShopeePay"},
	{"COM.OVO", "OVO"}, {"ID.LINKAJA", "LinkAja"}, {"ID.CO.CONTOH", "Contoh"},
}

// QRISDetailsOf reads the merchant details of a payload. Missing or invalid fields are "".
func QRISDetailsOf(s string) QRISDetails {
	var d QRISDetails
	tlvs, err := qrisParse(strings.TrimSpace(s))
	if err != nil {
		return d
	}
	var domain string
	for _, t := range tlvs {
		switch {
		case t.tag == "59":
			d.MerchantName = t.val
		case t.tag == "60":
			d.City = t.val
		case t.tag == "51":
			if subs, err := qrisParse(t.val); err == nil {
				for _, u := range subs {
					if u.tag == "02" {
						d.NMID = u.val
					}
				}
			}
		case len(t.tag) == 2 && t.tag >= "26" && t.tag <= "45" && domain == "":
			if subs, err := qrisParse(t.val); err == nil {
				for _, u := range subs {
					if u.tag == "00" {
						domain = strings.ToUpper(u.val)
					}
				}
			}
		}
	}
	if domain != "" {
		d.Provider = domain
		for _, p := range providers {
			if strings.HasPrefix(domain, p.prefix) {
				d.Provider = p.name
				break
			}
		}
	}
	return d
}

// QRISInfo returns the merchant name (tag 59) and NMID (tag 51, sub-tag 02) of a payload, "" when absent.
func QRISInfo(s string) (merchant, nmid string) {
	d := QRISDetailsOf(s)
	return d.MerchantName, d.NMID
}

// QRISToken is the public URL token of transaction id's QRIS page: 16 hex chars of HMAC-SHA256.
func QRISToken(key []byte, id int64) string {
	h := hmac.New(sha256.New, key)
	h.Write([]byte("qris:" + strconv.FormatInt(id, 10)))
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// DummyQRIS is a sample static QRIS of exactly length characters (when length is above the fixed
// part, about 125), with a valid CRC. It has the EMV structure of a real code but fake merchant
// data, so previews and tests never show a merchant's own code. Spare length goes into tag 99.
func DummyQRIS(length int) string {
	name := "CONTOH QRIS"
	merchant := tlvOf("00", "ID.CO.CONTOH.WWW", "01", "936000000001", "02", "ID1000000001")
	fixed := func(name string) string {
		return tlvOf("00", "01", "01", "11", "26", merchant, "52", "5499", "53", "360", "58", "ID",
			"59", name, "60", "CONTOH") + "6304"
	}
	pad := length - (len(fixed(name)) + 4)
	if pad > 0 && pad < 4 { // a tag needs 4 characters, so the name takes the rest
		name += strings.Repeat(" ", pad)
		pad = 0
	}
	var b strings.Builder
	for pad > 0 {
		c := min(pad, 103) // tag 99 holds at most 99 characters
		if r := pad - c; r > 0 && r < 4 {
			c = pad - 4
		}
		fmt.Fprintf(&b, "99%02d%s", c-4, strings.Repeat("X", c-4))
		pad -= c
	}
	s := fixed(name)
	s = s[:len(s)-4] + b.String() + "6304"
	return s + fmt.Sprintf("%04X", crc16(s))
}

// tlvOf encodes alternating tag and value pairs as EMV TLV.
func tlvOf(kv ...string) string {
	var b strings.Builder
	for i := 0; i < len(kv); i += 2 {
		fmt.Fprintf(&b, "%s%02d%s", kv[i], len(kv[i+1]), kv[i+1])
	}
	return b.String()
}
