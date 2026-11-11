package core

// Customer credential rules shared by the portal and the admin forms.

import (
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

// BcryptCost is lowered by tests; bcrypt at the default cost dominates test time under -race.
var BcryptCost = bcrypt.DefaultCost

// commonPasswords are refused for every customer password (compared lower case).
var commonPasswords = map[string]bool{"1234": true, "12345": true, "123456": true, "0000": true, "1111": true, "2222": true,

	"1212": true, "4321": true, "abcd": true, "qwer": true, "asdf": true, "password": true, "admin": true, "1q2w": true,
	"aaaa": true, "qwerty": true, "123123": true, "112233": true}

// CustomerPasswordMin is the setting customer_password_min (4 by default, 4 to 64).
func CustomerPasswordMin(st map[string]string) int {
	if n, err := strconv.Atoi(st["customer_password_min"]); err == nil && n >= 4 && n <= 64 {
		return n
	}
	return 4
}

// CustomerPasswordErr returns why pass cannot be a customer password, or "". It applies to every
// customer password path. Equal to the username is refused because RADIUS treats it as a voucher login.
func (s *Core) CustomerPasswordErr(st map[string]string, username, pass string) string {
	if n := CustomerPasswordMin(st); len(pass) < n || len(pass) > 64 {
		return fmt.Sprintf(s.T("Password should be between %d to 64 characters"), n)
	}
	if strings.EqualFold(pass, username) {
		return "Password must not be the same as the username"
	}
	if commonPasswords[strings.ToLower(pass)] {
		return "Password is too easy to guess"
	}
	return ""
}

// CustomerLoginURL is the customer login address for messages: app_url plus /login, or "" without app_url.
func CustomerLoginURL(st map[string]string) string {
	if u := strings.TrimRight(st["app_url"], "/"); u != "" {
		return u + "/login"
	}
	return ""
}
