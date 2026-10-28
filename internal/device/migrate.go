package device

// The router side of the migration from local accounts to RADIUS. Nothing here removes an account
// or an active session: RouterOS keeps a running session until it ends.

import "context"

// LocalAccount is a PPPoE secret or hotspot user with the id and state the migration needs.
type LocalAccount struct {
	Type, Name, Password, ID string
	Disabled                 bool
}

var accountPaths = map[string]string{"PPPoE": "/ppp/secret", "Hotspot": "/ip/hotspot/user"}

// LocalAccounts reads every secret and hotspot user with its id and disabled flag. It changes nothing.
func (r Router) LocalAccounts(ctx context.Context) ([]LocalAccount, error) {
	var out []LocalAccount
	for _, typ := range []string{"PPPoE", "Hotspot"} {
		rows, err := r.exec(ctx, []string{accountPaths[typ] + "/print", "=.proplist=.id,name,password,disabled"})
		if err != nil {
			return nil, err
		}
		for _, m := range rows {
			out = append(out, LocalAccount{typ, m["name"], m["password"], m[".id"], m["disabled"] == "true" || m["disabled"] == "yes"})
		}
	}
	return out, nil
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// SetDisabled disables or enables one local account (never removes it).
func (r Router) SetDisabled(ctx context.Context, a LocalAccount, disabled bool) error {
	_, err := r.exec(ctx, []string{accountPaths[a.Type] + "/set", "=numbers=" + a.ID, "=disabled=" + yesNo(disabled)})
	return err
}

// SetUseRadius sets use-radius on the PPP AAA settings and/or on every hotspot profile.
// ponytail: all hotspot profiles, not only the one a server uses; narrow it if a router mixes modes.
func (r Router) SetUseRadius(ctx context.Context, ppp, hotspot, on bool) error {
	if ppp {
		if _, err := r.exec(ctx, []string{"/ppp/aaa/set", "=use-radius=" + yesNo(on)}); err != nil {
			return err
		}
	}
	if hotspot {
		rows, err := r.exec(ctx, []string{"/ip/hotspot/profile/print", "=.proplist=.id"})
		if err != nil {
			return err
		}
		for _, m := range rows {
			if _, err := r.exec(ctx, []string{"/ip/hotspot/profile/set", "=numbers=" + m[".id"], "=use-radius=" + yesNo(on)}); err != nil {
				return err
			}
		}
	}
	return nil
}
