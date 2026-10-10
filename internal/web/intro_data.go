package web

// Static intro texts for list, form and page screens.

// Intro callouts. Texts are English catalog keys; Indonesian lives in lang/indonesia.json.

var listIntros = map[string]intro{
	"/admin/customers":             {"Your customers", "Everyone who buys internet from you. Open a name to recharge a plan, add balance or check the connection.", "Add a customer first, then pick a plan on the customer page. Without a plan there is no internet."},
	"/admin/subscriptions":         {"Customer plans", "Who has which plan and when it ends. Sort by the end date to see who needs to renew soon.", ""},
	"/admin/vouchers":              {"Vouchers", "Prepaid codes you print and sell. The buyer types the code and gets the plan, no cash handling afterwards.", "A voucher always belongs to one Service Plan, so create the plan first."},
	"/admin/transactions":          {"Payment history", "Every recharge and top-up that was recorded. Open an invoice to print it or show it to the customer.", ""},
	"/admin/plans":                 {"Service plans", "What you sell: a name, a price, how long it lasts and how fast it is. Customers and vouchers always use a plan.", "Create the Bandwidth (speed) first, then pick it when you add a plan."},
	"/admin/bandwidth":             {"Bandwidth (speed limits)", "A speed setting, for example 10 Mbps download and 5 Mbps upload. A plan uses it to decide how fast the customer goes.", "Make the Bandwidth first, then create the Service Plan that uses it."},
	"/admin/routers":               {"Routers", "The MikroTik devices that give your customers internet. This app talks to them to switch customers on and off.", "After saving, press Test connection. On the MikroTik the API service (port 8728) must be turned on."},
	"/admin/pool":                  {"IP pools", "A pool is a range of IP addresses handed out to PPPoE customers, for example 10.10.10.2-10.10.10.254.", "Only PPPoE plans need a pool. If you only use hotspot you can skip this."},
	"/admin/nas":                   {"RADIUS routers (NAS)", "A NAS is a router that asks this server to approve customer logins (RADIUS). Add one only if you use RADIUS instead of linking the router directly.", "The shared secret here must be exactly the same as the one set on the router."},
	"/admin/odp":                   {"ODP points", "An ODP is the fibre box on a pole where customer cables branch off. Record where each one is to find faults faster.", ""},
	"/admin/coupons":               {"Coupons", "Discount codes customers type when they buy. Set the value, the dates it works and how many times it may be used.", ""},
	"/admin/users":                 {"Admin users", "People who may sign in to this admin panel. Give each person the smallest role they need.", ""},
	"/admin/fields":                {"Extra customer fields", "Extra questions on the customer form, such as ID card number or tower name.", ""},
	"/admin/logs":                  {"Activity log", "What admins did, newest first. Look here to find out who changed or deleted something.", ""},
	"/admin/logs/radius":           {"RADIUS login log", "Login attempts from RADIUS customers, accepted or refused. Check it when a customer says they cannot connect.", ""},
	"/admin/logs/messages":         {"Message log", "Every SMS, WhatsApp or email this system sent, and whether it went through.", ""},
	"/admin/message/bulk/status":   {"Bulk message progress", "Who received your bulk message and who did not.", ""},
	"/admin/payment-gateway":       {"Online payment", "The online payment services customers can pay with. Keys are set under Settings, Payment Gateway.", ""},
	"/admin/payment-gateway/audit": {"Payment audit", "Raw payment notifications received from the gateway. For technicians checking a payment that never arrived.", ""},
}

var formIntros = map[string]intro{
	"/admin/customers":         {"Customer details", "Only username, password and name are required. The rest can be filled in later.", "Router secret is the password the MikroTik checks for PPPoE logins. Leave it empty for hotspot customers."},
	"/admin/bandwidth":         {"Speed setting", "Give the speed a name you will recognise, like 10M, then set download and upload.", ""},
	"/admin/plans":             {"Plan details", "Choose the type (Hotspot or PPPoE), the price and how long it lasts, then pick the Bandwidth and the Router.", "Period billing means the customer pays on the same day every month instead of after a fixed number of days. Router and Pool can stay empty for RADIUS plans."},
	"/admin/routers":           {"Router details", "Enter how to reach the MikroTik: its IP address, the API port (usually 8728) and a user that is allowed to change settings.", "Make a separate API user on the MikroTik instead of using the main admin login."},
	"/admin/pool":              {"Pool details", "Name the range and write it as start-end, for example 10.10.10.2-10.10.10.254, then pick its router.", ""},
	"/admin/nas":               {"NAS details", "Enter the router's IP address and the shared secret used for RADIUS.", "Use the same secret on the router and here, or every login will be refused."},
	"/admin/odp":               {"ODP details", "Name the fibre box and mark where it stands. Tap the map to set the location.", ""},
	"/admin/coupons":           {"Coupon details", "Choose a code, how much it takes off and when it works.", ""},
	"/admin/users":             {"User details", "Pick a role: Admin manages everything, Agent and Sales only serve customers.", "Use a strong password and never share one account between two people."},
	"/admin/fields":            {"Extra field", "Name the extra question and choose its type, such as text or number.", ""},
	"/admin/vouchers":          {"Make vouchers", "Pick a plan and how many codes to make. Leave Print now ticked to open the printable sheet right away.", "The code shape options are optional. The defaults work fine."},
	"/admin/vouchers/redeem":   {"Use a voucher for a customer", "Type a customer and a voucher code to give them the plan without cash.", ""},
	"/admin/deposit":           {"Add balance", "Put money into a customer's balance. They can later pay for plans from it.", "Type an amount, or pick a Balance plan, not both."},
	"/admin/subscriptions":     {"Edit a customer plan", "Change the dates of a customer's plan by hand. Use it only to fix mistakes.", "After changing it, press Sync in the list so the router matches."},
	"/admin/message/send":      {"Send a message", "Send one SMS, WhatsApp or email to a customer.", "The sending service must be set up under Settings first, or nothing will go out."},
	"/admin/message/bulk":      {"Bulk message", "Send the same message to many customers at once, filtered by router or status.", ""},
	"/admin/message/selected":  {"Message to selected customers", "Write one message to the customers you ticked in the list.", ""},
	"/admin/customers/message": {"Message to selected customers", "Write one message to the customers you ticked in the list.", ""},
	"/admin/password":          {"Change password", "Change your own password. Choose one others cannot guess.", ""},
	"/admin/pages":             {"Customer pages", "Text shown to customers on their portal, such as the announcement or help page.", ""},
	"/admin/settings":          {"App settings", "Options for the whole app. Start with General: company name, phone number and time zone.", "Changes apply to everyone as soon as you save."},
}

var pageIntros = map[string]intro{
	"report":          {"Daily report", "Money received on one day. Pick a date to compare, and print to keep a copy.", ""},
	"report_period":   {"Period report", "Money received between two dates. Good for monthly closing.", ""},
	"radius_sessions": {"Online now (RADIUS)", "Customers connected right now through RADIUS. Disconnect (CoA) asks the router to drop one so they reconnect with their latest plan.", "Customers who connect through the router API are not listed here. Open their customer page instead."},
	"recharge":        {"Recharge Account", "Add a plan to a customer. Type the customer's username, pick the plan and how they paid, then check the summary before saving.", "You can also do this from the customer page."},
	"maps":            {"Map", "Pins show where customers, routers or ODP boxes are. Tap a pin for its details.", ""},
	"docs":            {"Guide", "Step-by-step notes for the operator: install, settings, MikroTik and RADIUS, moving from PHPNuxBill, and security. Pick a topic on the left.", ""},
	"network":         {"Network health", "Every router and RADIUS NAS with its status. Open one to see its details. Press Check now to ask a router for its live state.", "Live stats only come from routers added with an API user. A NAS only shows what the RADIUS side has seen."},
	"p_dashboard":     {"Your internet account", "Your balance, your plans and the date each one ends. Press Buy / Extend before it ends so you stay online.", ""},
}
