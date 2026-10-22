package radius

import (
	"crypto/hmac"
	"crypto/md5"
	"testing"

	"layeh.com/radius"
	"layeh.com/radius/rfc2865"
)

// RFC 5176 §3.5: the Message-Authenticator of a Disconnect- or CoA-Request is computed with a zeroed
// Request Authenticator. RouterOS silently drops a CoA whose Message-Authenticator is wrong.
func TestRequestMAUsesZeroAuthenticator(t *testing.T) {
	for _, code := range []radius.Code{radius.CodeDisconnectRequest, radius.CodeCoARequest} {
		p := radius.New(code, []byte("s3cret"))
		rfc2865.UserName_SetString(p, "alice")
		addMA(p)
		b, err := p.Encode()
		if err != nil {
			t.Fatal(err)
		}
		// b[0:20] header, b[20:22] MA type/len, b[22:38] MA value (addMA puts it first).
		got := append([]byte(nil), b[22:38]...)
		z := append([]byte(nil), b...)
		clear(z[4:20])
		clear(z[22:38])
		m := hmac.New(md5.New, []byte("s3cret"))
		m.Write(z)
		if !hmac.Equal(got, m.Sum(nil)) {
			t.Errorf("%v: Message-Authenticator not computed over a zero authenticator", code)
		}
	}
}
