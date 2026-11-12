package customers

import "github.com/frand-kod/gobill/internal/web/admin/adminx"

// Short local names for the shared form and list models.
type (
	field      = adminx.Field
	formPage   = adminx.FormPage
	option     = adminx.Option
	listPage   = adminx.ListPage
	listRow    = adminx.ListRow
	filter     = adminx.Filter
	rowAction  = adminx.RowAction
	bulkAction = adminx.BulkAction
)

var (
	section      = adminx.Section
	text         = adminx.Text
	customerPick = adminx.CustomerPick
	wholeErr     = adminx.WholeErr
	row          = adminx.Row
	listSort     = adminx.ListSort
	jsonStr      = adminx.JSONStr
)
