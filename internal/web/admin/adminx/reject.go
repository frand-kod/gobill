package adminx

// RejectLabels maps the reject reasons of radius.Authorize to English keys (translated by T); other reasons are shown as they are.
var RejectLabels = map[string]string{
	"Login invalid......":                "Login refused",
	"Too many attempts, try again later": "Too many attempts, try again later",
	"Internet Plan Expired..":            "Internet plan expired",
	"Voucher Expired...":                 "Voucher expired",
	"You have exceeded your time limit.": "Time limit reached",
	"You have exceeded your data limit.": "Data limit reached",
}
