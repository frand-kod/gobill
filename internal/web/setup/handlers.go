// Package setup is the Quick Setup wizard: its pages, the saves of each step and the progress state.
// It replays the admin handlers of network, plans, billing and settings, so validation stays in one place.
package setup

import (
	"github.com/frand-kod/gobill/internal/service/plan"
	"github.com/frand-kod/gobill/internal/web/admin/adminx"
	"github.com/frand-kod/gobill/internal/web/admin/billing"
	"github.com/frand-kod/gobill/internal/web/admin/network"
	"github.com/frand-kod/gobill/internal/web/admin/plans"
	adminsettings "github.com/frand-kod/gobill/internal/web/admin/settings"
	"github.com/frand-kod/gobill/internal/web/core"
)

// Handlers serves the wizard.
type Handlers struct {
	*adminx.Base
	NetH      *network.Handlers
	PlansH    *plans.Handlers
	BillingH  *billing.Handlers
	SettingsH *adminsettings.Handlers
	PlanSvc   *plan.Service
}

// Short local names for the shared form models.
type (
	field    = adminx.Field
	formPage = adminx.FormPage
	option   = adminx.Option
	setupNav = core.SetupNav
)

var (
	text    = adminx.Text
	section = adminx.Section
)

const msgRouterOther = adminx.MsgRouterOther

var routerErr = adminx.RouterErr
