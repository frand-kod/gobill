package network

import "github.com/frand-kod/gobill/internal/web/admin/adminx"

type (
	field     = adminx.Field
	formPage  = adminx.FormPage
	option    = adminx.Option
	listRow   = adminx.ListRow
	listPage  = adminx.ListPage
	rowAction = adminx.RowAction
)

const msgRouterOther = adminx.MsgRouterOther

var (
	text      = adminx.Text
	section   = adminx.Section
	routerErr = adminx.RouterErr
	jsonStr   = adminx.JSONStr
)
