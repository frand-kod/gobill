package core

import "github.com/frand-kod/gobill/internal/db"

// Intro is the short "what is this page" callout shown above the content. Title, Text and Tip are
// English catalog keys (translated in render); Tip is only set where there is a common pitfall.
type Intro struct{ Title, Text, Tip string }

// Option is a value and its label, for selects and tab menus.
type Option struct{ Value, Label string }

// FormHelp is what a form page adds around its fields (set on Page.Help): a docs link in the header,
// an Alpine expression for a live summary line, a narrower card and a save bar that sticks to the bottom.
type FormHelp struct {
	Docs, Summary  string
	Narrow, Sticky bool
}

// SetupNav is what the layout shows a SuperAdmin: the sidebar item, the first-run dialog and the progress badge.
type SetupNav struct {
	Show     bool // sidebar item: not finished and not dismissed
	Modal    bool // first page load: offer the wizard
	Done     int
	Total    int
	Finished bool // done or dismissed: the Guide links to the wizard
}

// AttnItem is one admin bell row: Kind is its English label (the template translates it), Count how many,
// Href where it leads. Err marks a service that is down (red); Mgr hides it below Admin.
type AttnItem struct {
	Kind  string
	Count int
	Href  string
	Err   bool
	Mgr   bool
}

// BellItem is one line of the portal bell. Ver changes when the content does, which makes it unread again.
type BellItem struct {
	Key, Ver, Title, Text, Href string
	Unread                      bool
}

// Page is the data every template receives.
type Page struct {
	Title         string
	Admin         *db.Admin
	Customer      *db.Customer // portal customer, if any
	Impersonating bool         // portal session opened by an admin (login as customer)
	Unread        int64        // portal inbox badge
	Bell          []BellItem   // portal bell items
	BellUnread    int          // how many of them are unread
	Flash         string
	Sticky        bool // the flash carries a money result: no auto-dismiss
	Error         string
	Warn          string   // yellow alert: the action worked but needs a follow-up
	Detail        string   // raw technical text of Error, shown in a small <details>
	ErrLink       string   // href of an "Add balance" button next to Error
	Intro         Intro    // callout under the page title; filled by Decorate when empty
	Help          FormHelp // form pages: docs link, summary line, narrow card, sticky save bar
	Path          string
	Tabs          []Option          // settings sub-page menu
	Brand         map[string]string // logo and login page text, filled by Decorate
	Setup         *SetupNav         // Quick Setup state of a SuperAdmin page, filled by Decorate
	Dir           string            // "rtl" or "ltr"
	Lang          string
	Version       string
	UpdateTag     string // set by Decorate for SuperAdmin when a newer release is known
	UpdateURL     string
	Notices       []AttnItem // admin bell: the items this admin may see, filled by Decorate for admin pages
	NoticeTotal   int        // sum of the Notices counts, the bell badge
	NoticeErr     bool       // one of Notices is an error: the badge turns red
	Data          any
}
