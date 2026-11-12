package radius

import (
	"bytes"
	"crypto/md5"
	"crypto/subtle"

	"layeh.com/radius"
	"layeh.com/radius/rfc2759"
	"layeh.com/radius/rfc2865"
	"layeh.com/radius/vendors/microsoft"
)

const badPassword = "Username or Password is wrong"

// CHAPOK checks a CHAP response: md5(id || password || challenge) == resp.
func CHAPOK(pw []byte, id byte, challenge, resp []byte) bool {
	h := md5.New()
	h.Write([]byte{id})
	h.Write(pw)
	h.Write(challenge)
	return subtle.ConstantTimeCompare(h.Sum(nil), resp) == 1
}

// checkPassword tries CHAP, MS-CHAPv2, then PAP. success is the MS-CHAP2-Success blob when applicable.
func checkPassword(p *radius.Packet, user string, pw []byte) (ok bool, success string) {
	if chap := rfc2865.CHAPPassword_Get(p); len(chap) == 17 {
		ch := rfc2865.CHAPChallenge_Get(p)
		if len(ch) == 0 {
			ch = p.Authenticator[:]
		}
		return CHAPOK(pw, chap[0], ch, chap[1:]), ""
	}
	if resp := microsoft.MSCHAP2Response_Get(p); len(resp) == 50 {
		ch := microsoft.MSCHAPChallenge_Get(p)
		if len(ch) != 16 {
			return false, ""
		}
		nt, err := rfc2759.GenerateNTResponse(ch, resp[2:18], []byte(user), pw)
		if err != nil || !bytes.Equal(nt, resp[26:50]) {
			return false, ""
		}
		auth, err := rfc2759.GenerateAuthenticatorResponse(ch, resp[2:18], nt, []byte(user), pw)
		if err != nil {
			return false, ""
		}
		// ponytail: MPPE keys (MS-MPPE-Send/Recv) not sent; add if PPPoE needs encryption.
		return true, string(resp[:1]) + auth
	}
	if _, err := rfc2865.UserPassword_Lookup(p); err == nil {
		return subtle.ConstantTimeCompare([]byte(rfc2865.UserPassword_GetString(p)), pw) == 1, ""
	}
	return false, ""
}
