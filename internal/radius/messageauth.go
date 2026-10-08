package radius

import (
	"crypto/hmac"
	"crypto/md5"

	"layeh.com/radius"
	"layeh.com/radius/rfc2869"
)

// maSum is the RFC 3579 section 3.2 HMAC-MD5 of p with the Message-Authenticator zeroed. The
// authenticator field is the Request Authenticator for responses (what Response() copies in)
// and all zeros for Disconnect-Request. The Message-Authenticator is taken from p as it is.
func maSum(p *radius.Packet) []byte {
	var saved [16]byte
	if p.Code == radius.CodeDisconnectRequest {
		saved, p.Authenticator = p.Authenticator, [16]byte{}
		defer func() { p.Authenticator = saved }()
	}
	var old []byte
	for _, a := range p.Attributes {
		if a.Type == rfc2869.MessageAuthenticator_Type {
			old = append([]byte(nil), a.Attribute...)
			clear(a.Attribute)
			defer func(a *radius.AVP) { copy(a.Attribute, old) }(a)
			break
		}
	}
	b, err := p.MarshalBinary()
	if err != nil {
		return nil
	}
	m := hmac.New(md5.New, p.Secret)
	m.Write(b)
	return m.Sum(nil)
}

// addMA puts a Message-Authenticator first (BlastRADIUS advice) in an outgoing packet. Call it
// before Encode, which computes the Response Authenticator over the finished attributes.
func addMA(p *radius.Packet) {
	p.Del(rfc2869.MessageAuthenticator_Type)
	p.Attributes = append(radius.Attributes{{Type: rfc2869.MessageAuthenticator_Type, Attribute: make([]byte, 16)}}, p.Attributes...)
	copy(p.Attributes[0].Attribute, maSum(p))
}

// checkMA reports whether a request is acceptable: a present Message-Authenticator must verify;
// a missing one is fine unless required.
func checkMA(p *radius.Packet, required bool) bool {
	got, err := rfc2869.MessageAuthenticator_Lookup(p)
	if err != nil {
		return !required
	}
	return len(got) == 16 && hmac.Equal(got, maSum(p))
}

// send signs and writes an Access-* response.
func send(w radius.ResponseWriter, p *radius.Packet) error {
	addMA(p)
	return w.Write(p)
}
