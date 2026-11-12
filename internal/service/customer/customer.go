// Package customer holds the customer write logic: validation, password hashing, secret sealing
// and the transaction that saves a customer with its custom field values and port binding.
package customer

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"net/mail"
	"slices"
	"strconv"
	"strings"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/radius"
	"github.com/frand-kod/gobill/internal/secret"
	"github.com/frand-kod/gobill/internal/settings"
	"golang.org/x/crypto/bcrypt"
)

// MsgPhoneTaken is the error for a WhatsApp number another account already uses (wa_unique_per_account = yes).
const MsgPhoneTaken = "This WhatsApp number is already used by another account."

var Statuses = []string{"Active", "Banned", "Disabled", "Inactive", "Limited", "Suspended"}

// Service needs the same deps the web server holds.
type Service struct {
	DB         *sql.DB
	Q          *db.Queries
	Key        []byte
	BcryptCost int // 0 is bcrypt.DefaultCost
}

// CFValue is one custom field value to store with the customer.
type CFValue struct {
	FieldID int64
	Value   string
}

// SaveInput is one create (ID 0) or update. Messages in Errs are untranslated keys.
type SaveInput struct {
	ID      int64
	Cur     db.Customer       // the stored customer on update
	V       map[string]string // form values; phone, bind_mac and bind_port_id are normalised in place
	Pass    string
	Secret  string
	AdminID int64
	CF      []CFValue
	// Errs holds the errors the caller found already (password policy, custom fields, plan).
	// The service adds its own and saves only if the map is still empty afterwards.
	Errs map[string]string
}

// Result: Saved is false when Errs is not empty.
type Result struct {
	ID      int64
	Created db.Customer // the new row on create
	Saved   bool
}

func (s *Service) cost() int { return cmp.Or(s.BcryptCost, bcrypt.DefaultCost) }

// parseCoords reads "lat,lng". ok is false unless both are numbers in range.
func parseCoords(s string) (lat, lng float64, ok bool) {
	a, b, found := strings.Cut(s, ",")
	if !found {
		return 0, 0, false
	}
	lat, err1 := strconv.ParseFloat(strings.TrimSpace(a), 64)
	lng, err2 := strconv.ParseFloat(strings.TrimSpace(b), 64)
	ok = err1 == nil && err2 == nil && lat >= -90 && lat <= 90 && lng >= -180 && lng <= 180
	return lat, lng, ok
}

// checkCoords validates an optional "lat,lng" and returns it normalized; "" stays "".
func checkCoords(s string) (string, bool) {
	if s == "" {
		return "", true
	}
	lat, lng, ok := parseCoords(s)
	if !ok {
		return s, false
	}
	return strconv.FormatFloat(lat, 'f', -1, 64) + "," + strconv.FormatFloat(lng, 'f', -1, 64), true
}

// wholeErr is the message for a whole-number field given a decimal ("12.5", "12,5"); "" for anything else.
func wholeErr(v string) string {
	if strings.ContainsAny(v, ".,") {
		return "Use whole numbers without dots or commas"
	}
	return ""
}

// PhoneTaken reports whether another customer uses this WhatsApp number. Only checked when
// wa_unique_per_account is yes: by default several accounts may share one number (family, RT/RW).
func (s *Service) PhoneTaken(ctx context.Context, phone string, self int64) (bool, error) {
	if v := settings.Get(ctx, s.Q, "wa_unique_per_account"); v != "yes" {
		return false, nil
	}
	want := db.CanonicalPhone(phone)
	if want == "" {
		return false, nil
	}
	// ponytail: loads every phone per check; compare in SQL if customers grow large.
	rows, err := s.Q.ListCustomerPhones(ctx)
	if err != nil {
		return false, err
	}
	for _, c := range rows {
		if c.ID != self && db.CanonicalPhone(c.Phone) == want {
			return true, nil
		}
	}
	return false, nil
}

// SetPassword stores a new portal password and, as in the old app (one password for portal and
// router), the same value as the router secret.
func (s *Service) SetPassword(ctx context.Context, id int64, pass string) error {
	hash, err := bcrypt.GenerateFromPassword([]byte(pass), s.cost())
	if err != nil {
		return err
	}
	enc, err := secret.Seal(s.Key, []byte(pass))
	if err != nil {
		return err
	}
	if err = s.Q.SetCustomerPassword(ctx, db.SetCustomerPasswordParams{PasswordHash: string(hash), ID: id}); err != nil {
		return err
	}
	return s.Q.SetCustomerSecret(ctx, db.SetCustomerSecretParams{SecretEnc: enc, ID: id})
}

// Save validates in.V, adds problems to in.Errs and, if there are none, writes the customer.
// A unique clash on the username at commit time is reported in Errs too.
func (s *Service) Save(ctx context.Context, in *SaveInput) (Result, error) {
	v, e, id, cur := in.V, in.Errs, in.ID, in.Cur
	v["phone"] = db.CanonicalPhone(v["phone"])
	if v["username"] == "" {
		e["username"] = "This field is required"
	}
	// RADIUS logs in by username or pppoe_username, so neither may collide with another customer.
	for _, f := range []string{"username", "pppoe_username"} {
		if _, ok := e[f]; ok || v[f] == "" || id != 0 && v[f] == cur.Username && f == "username" || id != 0 && v[f] == cur.PppoeUsername && f == "pppoe_username" {
			continue
		}
		if _, err := s.Q.FindLoginNameOwner(ctx, db.FindLoginNameOwnerParams{Self: id, Name: v[f]}); err == nil {
			e[f] = "Username already used by another customer"
		} else if !errors.Is(err, sql.ErrNoRows) {
			return Result{}, err
		}
	}
	if v["bind_mac"] != "" {
		if m := radius.NormMAC(v["bind_mac"]); m != "" {
			v["bind_mac"] = m
		} else {
			e["bind_mac"] = "Enter a MAC address like AA:BB:CC:DD:EE:FF"
		}
	}
	v["bind_port_id"] = strings.TrimSpace(v["bind_port_id"])
	if v["phone"] != "" { // several accounts may share a number unless wa_unique_per_account is yes
		if taken, err := s.PhoneTaken(ctx, v["phone"], id); err != nil {
			return Result{}, err
		} else if taken {
			e["phone"] = MsgPhoneTaken
		}
	}
	if v["fullname"] == "" {
		e["fullname"] = "This field is required"
	}
	if v["email"] != "" {
		if _, err := mail.ParseAddress(v["email"]); err != nil {
			e["email"] = "Invalid email address"
		}
	}
	if !slices.Contains([]string{"Hotspot", "PPPoE", "Others"}, v["service_type"]) {
		e["service_type"] = "Invalid value"
	}
	if !slices.Contains(Statuses, v["status"]) {
		e["status"] = "Invalid value"
	}
	coords, ok := checkCoords(v["coordinates"])
	if !ok {
		e["coordinates"] = "Enter coordinates as lat,lng, e.g. -6.2,106.8"
	}
	bday := sql.NullInt64{}
	if v["billing_day"] != "" {
		n, err := strconv.ParseInt(v["billing_day"], 10, 64)
		if err != nil || n < 1 || n > 31 {
			e["billing_day"] = cmp.Or(wholeErr(v["billing_day"]), "Enter a day between 1 and 31")
		}
		bday = sql.NullInt64{Int64: n, Valid: true}
	}
	if len(e) > 0 {
		return Result{}, nil
	}

	enc := cur.SecretEnc
	if rs := cmp.Or(in.Secret, in.Pass); rs != "" { // one password like the old app; explicit secret wins
		var err error
		if enc, err = secret.Seal(s.Key, []byte(rs)); err != nil {
			return Result{}, err
		}
	}
	renew := int64(0)
	if v["auto_renewal"] == "1" {
		renew = 1
	}
	var hash []byte // hashed before the tx: BeginTx takes the write lock (_txlock=immediate)
	if id == 0 || in.Pass != "" {
		var err error
		if hash, err = bcrypt.GenerateFromPassword([]byte(in.Pass), s.cost()); err != nil {
			return Result{}, err
		}
	}
	tx, err := s.DB.BeginTx(ctx, nil) // customer row and custom field values commit together
	if err != nil {
		return Result{}, err
	}
	defer tx.Rollback()
	q := s.Q.WithTx(tx)
	cid := id
	var created db.Customer
	if id == 0 {
		var c db.Customer
		c, err = q.CreateCustomer(ctx, db.CreateCustomerParams{Username: v["username"], PasswordHash: string(hash),
			Fullname: v["fullname"], Address: v["address"], Phone: v["phone"], Email: v["email"], ServiceType: v["service_type"],
			PppoeUsername: v["pppoe_username"], PppoeIp: v["pppoe_ip"], SecretEnc: enc, AutoRenewal: renew,
			Status: v["status"], CreatedBy: sql.NullInt64{Int64: in.AdminID, Valid: true}, BillingDay: bday, Coordinates: coords})
		cid, created = c.ID, c
	} else {
		if v["username"] != cur.Username { // first, so a clash changes nothing
			err = q.RenameCustomer(ctx, db.RenameCustomerParams{Username: v["username"], ID: id})
		}
		if err == nil {
			err = q.UpdateCustomer(ctx, db.UpdateCustomerParams{Fullname: v["fullname"], Address: v["address"],
				Phone: v["phone"], Email: v["email"], ServiceType: v["service_type"], PppoeUsername: v["pppoe_username"],
				PppoeIp: v["pppoe_ip"], SecretEnc: enc, AutoRenewal: renew, Status: v["status"], BillingDay: bday, Coordinates: coords, ID: id})
		}
		if err == nil && in.Pass != "" {
			err = q.SetCustomerPassword(ctx, db.SetCustomerPasswordParams{PasswordHash: string(hash), ID: id})
		}
	}
	for _, x := range in.CF {
		if err == nil {
			err = q.UpsertCustomerFieldValue(ctx, db.UpsertCustomerFieldValueParams{CustomerID: cid, FieldID: x.FieldID, Value: x.Value})
		}
	}
	if err == nil {
		if v["bind_mac"] == "" && v["bind_port_id"] == "" {
			err = q.DeleteCustomerBinding(ctx, cid)
		} else {
			err = q.SetCustomerBinding(ctx, db.SetCustomerBindingParams{CustomerID: cid, Mac: v["bind_mac"], PortID: v["bind_port_id"]})
		}
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed") {
		e["username"] = "Username already exists"
		return Result{}, nil
	}
	if err != nil {
		return Result{}, err
	}
	return Result{ID: cid, Created: created, Saved: true}, nil
}
