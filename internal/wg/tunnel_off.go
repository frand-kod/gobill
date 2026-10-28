//go:build nowg

package wg

import "github.com/frand-kod/gobill/internal/db"

const built = false

type tunnel struct{}

func (tunnel) running() bool                   { return false }
func (tunnel) down()                           {}
func (tunnel) stats() map[string]Stat          { return nil }
func (tunnel) up(*Manager, Config) error       { return nil }
func (tunnel) addPeer(Config, db.WgPeer) error { return nil }
func (tunnel) removePeer(string) error         { return nil }
