package selfupdate

import (
	"context"
	"github.com/frand-kod/gobill/internal/settings"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/update"
)

const (
	updateCheckFirst = 3 * time.Minute // first look a few minutes after start
	updateCheckEvery = 24 * time.Hour  // then once a day
)

// Check asks GitHub for the latest gobill release and keeps the answer in memory for the
// SuperAdmin badge. It does nothing while the update_check setting is "no", and then drops the
// stored answer. A failed check is logged, not returned, so an offline box does not raise alerts.
type Check struct {
	Q      *db.Queries
	URL    string       // release endpoint; "" = update.APIURL + "/latest"
	Client *http.Client // nil = 30 s timeout
	Now    func() time.Time

	mu        sync.Mutex
	tag, link string
	due       time.Time // next check; only Run touches it
}

// Run is the job.Run body. It ticks every minute and checks only when due.
func (c *Check) Run(ctx context.Context) error {
	now := time.Now()
	if c.Now != nil {
		now = c.Now()
	}
	st, err := settings.List(ctx, c.Q)
	if err != nil {
		return err
	}
	if st["update_check"] == "no" {
		c.set("", "")
		return nil
	}
	if c.due.IsZero() {
		c.due = now.Add(updateCheckFirst)
	}
	if now.Before(c.due) {
		return nil
	}
	c.due = now.Add(updateCheckEvery)

	url := c.URL
	if url == "" {
		url = update.APIURL + "/latest"
	}
	client := c.Client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	rel, err := update.Fetch(ctx, client, url)
	if err != nil {
		slog.Warn("update check failed", "err", err)
		return nil
	}
	c.set(rel.Tag, rel.URL)
	return nil
}

// Latest returns the newest release tag and its release page, or "" when none is known.
func (c *Check) Latest() (tag, link string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.tag, c.link
}

func (c *Check) set(tag, link string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tag, c.link = tag, link
}
