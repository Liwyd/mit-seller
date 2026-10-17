package bot

import (
	"strings"

	"github.com/Liwyd/mit-seller/internal/panels"
	"github.com/Liwyd/mit-seller/internal/php"
)

// "نام کاربری من در لیست نیست": a user who bought a subscription outside the
// bot takes that config into their own list, so it shows up in their services
// and can be renewed, topped up and removed like any other.
//
// The panel records no Telegram identity for a config — every driver sends
// quota and credentials only, and x-ui even sends tgId as "" — so ownership
// has to come from somewhere else. When the bot sold the config, the default
// username method wrote the buyer's own Telegram id into the name
// (<id>_<random>, see generateUsername). That is proof enough to act on at
// once. Everything else is a claim in the strict sense and waits for an
// admin.
const (
	claimSourceAuto  = "claimed"
	claimSourceAdmin = "claimed_admin"

	// One claim a minute, and never more than this many waiting on an admin,
	// so a user cannot walk the panel's user list through this door.
	claimCooldownSeconds = 60
	claimMaxPending      = 3
)

// canAutoClaim is the whole of tier one, kept free of the bot so it can be
// reasoned about on its own.
//
// The separator matters: matching a bare prefix would let the holder of
// Telegram id 123 claim 1234_…'s config. Requiring the underscore after the
// exact id is what makes this safe, and it is the same string
// generateUsername produced at purchase time.
func canAutoClaim(username, fromID, method string) bool {
	if username == "" || fromID == "" {
		return false
	}
	if method != T("users.customidAndRandom") {
		return false
	}
	// And something has to follow the separator: generateUsername always
	// appends random bytes, so "<id>_" on its own was never sold to anyone.
	if len(username) <= len(fromID)+1 {
		return false
	}
	return strings.HasPrefix(username, fromID+"_")
}

// claimFound is whether the panel handed the config back at all.
//
// The status on a success is the account's own — active, expired, limited,
// on_hold, disabled — never a success word, so this must not be tested against
// "successful"; that would reject every config on every panel. Every driver
// reports failure through unsuccessful(), which stamps Unsuccessful and no
// username. This also catches an unreachable panel, unlike the old test which
// only looked for the words "User not found" and so let a network failure
// through as a match.
func claimFound(out panels.Out) bool {
	if !out.Isset("status") {
		return false
	}
	return !strings.EqualFold(out.S("status"), "Unsuccessful")
}

// claimVolume carries the panel's live limit across as MB, the same unit the
// buy path stores from Volume_constraint. Empty for unlimited, which is what
// the panel reports as a null or zero data_limit.
func claimVolume(out panels.Out) string {
	limit := out.F("data_limit")
	if limit <= 0 {
		return ""
	}
	return itoa(int64(limit / (1024 * 1024)))
}

// claimRateLimited is one claim a minute. It counts attempts rather than
// pending requests, and applies to auto-claims as well: it is the attempt to
// walk the panel's user list that is being limited, not the admin's inbox.
func (c *Ctx) claimRateLimited() bool {
	d := c.db()
	r := d.MustQuery("SELECT created_at FROM claim_request WHERE id_user = ? ORDER BY id_claim DESC LIMIT 1", c.fromID)
	if len(r) == 0 {
		return false
	}
	last := php.Intval(r[0].S("created_at"))
	return last > 0 && nowUnix()-last < claimCooldownSeconds
}

// claimQueueFull caps what one account may leave waiting on an admin. It
// belongs to tier two alone: a claim that proved its own ownership must not be
// turned away because somebody's other requests are still queued.
func (c *Ctx) claimQueueFull() bool {
	return c.db().Count("SELECT COUNT(*) FROM claim_request WHERE id_user = ? AND status = 'pending'", c.fromID) >= claimMaxPending
}

// claimAttach is the row that makes the config reachable: without an invoice
// with this user's id on it, nothing in the bot will ever list the service,
// and cron will never message them about it.
//
// It mirrors the admin's "add order" (admin3.go, stependforaddorder), the
// only other place a pre-existing config is attached to an account.
//
// name_product is a label rather than NULL on purpose: cron selects on
// name_product != 'usertest', and NULL != 'usertest' is NULL in MySQL, which
// would quietly switch off every expiry warning, renewal reminder and
// auto-removal for this service. Renew overwrites it with a real product on
// the first renewal, exactly as it does for an admin-added order.
func (c *Ctx) claimAttach(ownerID, username, namePanel, source, volume string) bool {
	d := c.db()
	// username is not a unique column, so INSERT IGNORE alone would not stop a
	// second row; check first, the way the admin flow does.
	if d.Exists("invoice", "username", username) {
		return false
	}
	// And IGNORE swallows a conflict rather than raising it, so success has to
	// be measured, not assumed: telling someone a service was attached when the
	// row never landed would leave them watching for it forever.
	res, err := d.Exec("INSERT IGNORE INTO invoice (id_user, id_invoice, username, time_sell, Service_location, name_product, price_product, Volume, Service_time, Status, source) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		ownerID, randHex(4), username, itoa(nowUnix()), namePanel,
		T("users.claim.productname"), "0", volume, "", "active", source)
	if err != nil {
		return false
	}
	if n, e := res.RowsAffected(); e == nil && n == 0 {
		return false
	}
	return true
}

// claimRecord writes the request itself, which is both the admin's queue and
// the audit trail of who claimed what and who let them.
func (c *Ctx) claimRecord(username, namePanel, method string) string {
	id := randHex(8)
	c.db().Exec("INSERT INTO claim_request (id_claim, id_user, username, name_panel, status, method, created_at) VALUES (?, ?, ?, ?, 'pending', ?, ?)",
		id, c.fromID, username, namePanel, method, itoa(nowUnix()))
	return id
}

// claimNotifyAdmins puts the request in front of every admin, which is where
// the other manual confirmations go too. The report channel is for sales and
// errors, not for decisions.
func (c *Ctx) claimNotifyAdmins(id, username, namePanel string) {
	msg := sprintf("Admin.claim.request", c.fromID, c.fromID, c.username, username, namePanel)
	kb := ik(
		row(cb(T("Admin.claim.approve"), "claimok_"+id), cb(T("Admin.claim.reject"), "claimno_"+id)),
	)
	for _, a := range c.adminIDs {
		c.sendHTML(a, msg, kb)
	}
}

// secClaim is the user half of the flow. It picks up where the username has
// been typed and the panel picked, which is the point at which the old
// read-only card used to be shown.
func (c *Ctx) secClaim(namePanel, username string, out panels.Out) bool {
	d := c.db()

	// Nothing to claim unless the panel actually handed the config back.
	if !claimFound(out) {
		c.send(c.fromID, T("users.status.notUsernameget"), c.kbMain(), "html")
		c.step("home")
		return true
	}
	// Take the name the panel knows it by, in case it normalised what was
	// typed: every later lookup keys on this exact string.
	if got := out.S("username"); got != "" {
		username = got
	}

	// Already somebody's in the bot? Then it is not the claimant's to take,
	// whatever the panel says. Being theirs already is a different answer
	// though — they only need telling it is in the list, not that someone else
	// has it.
	if r := d.MustQuery("SELECT id_user FROM invoice WHERE username = ? AND "+activeInvoiceWhere+" LIMIT 1", username); len(r) > 0 {
		if r[0].S("id_user") == c.fromID {
			c.sendHTML(c.fromID, sprintf("users.claim.ownlist", username), c.kbMain())
		} else {
			c.sendHTML(c.fromID, T("users.claim.alreadyowned"), c.kbMain())
		}
		c.step("home")
		return true
	}

	if c.claimRateLimited() {
		c.sendHTML(c.fromID, T("users.claim.throttled"), c.kbMain())
		c.step("home")
		return true
	}

	panel := d.Select("marzban_panel", "MethodUsername", "name_panel", namePanel)
	volume := claimVolume(out)

	if canAutoClaim(username, c.fromID, panel.S("MethodUsername")) {
		if !c.claimAttach(c.fromID, username, namePanel, claimSourceAuto, volume) {
			c.sendHTML(c.fromID, T("users.claim.alreadyowned"), c.kbMain())
			c.step("home")
			return true
		}
		c.db().Exec("INSERT INTO claim_request (id_claim, id_user, username, name_panel, status, method, created_at) VALUES (?, ?, ?, ?, 'approved', 'auto', ?)",
			randHex(8), c.fromID, username, namePanel, itoa(nowUnix()))
		c.sendHTML(c.fromID, sprintf("users.claim.attached", username), c.kbMain())
		c.step("home")
		return true
	}

	// Nothing proved here, so this is the request that has to fit within the
	// queue an admin actually works through.
	if c.claimQueueFull() {
		c.sendHTML(c.fromID, T("users.claim.throttled"), c.kbMain())
		c.step("home")
		return true
	}

	id := c.claimRecord(username, namePanel, "admin")
	c.claimNotifyAdmins(id, username, namePanel)
	c.sendHTML(c.fromID, sprintf("users.claim.pending", username), c.kbMain())
	c.step("home")
	return true
}

// admClaims is the admin half: the queue that arrives from tier two.
func (c *Ctx) admClaims() bool {
	d := c.db()

	if c.m(`claimok_(\w+)`) {
		id := c.g(1)
		r := d.Select("claim_request", "*", "id_claim", id)
		if r == nil || r.S("status") != "pending" {
			c.sendHTML(c.fromID, T("Admin.claim.missing"), nil)
			return true
		}
		// The claimant may have been removed, the config may since have been
		// sold to someone else inside the bot, or it may have gone from the
		// panel entirely while the request sat in the queue. Attaching a config
		// that is no longer there would hand the user a dead row they could
		// never act on, so all three are re-checked here.
		namePanel := r.S("name_panel")
		username := r.S("username")
		if d.Exists("invoice", "username", username) {
			c.sendHTML(c.fromID, T("Admin.claim.taken"), nil)
			d.Update("claim_request", "status", "rejected", "id_claim", id)
			d.Update("claim_request", "reason", T("Admin.claim.taken"), "id_claim", id)
			return true
		}
		out := c.b.PM.DataUser(namePanel, username)
		if !claimFound(out) {
			c.sendHTML(c.fromID, sprintf("Admin.claim.notfound", username), nil)
			d.Update("claim_request", "status", "rejected", "id_claim", id)
			d.Update("claim_request", "reason", T("Admin.claim.notfound"), "id_claim", id)
			return true
		}
		// Take the volume from the panel as it stands now rather than as it
		// stood when the request was filed — quota may have moved since.
		volume := claimVolume(out)
		if !c.claimAttach(r.S("id_user"), username, namePanel, claimSourceAdmin, volume) {
			c.sendHTML(c.fromID, T("Admin.claim.taken"), nil)
			d.Update("claim_request", "status", "rejected", "id_claim", id)
			d.Update("claim_request", "reason", T("Admin.claim.taken"), "id_claim", id)
			return true
		}
		d.Update("claim_request", "status", "approved", "id_claim", id)
		d.Update("claim_request", "reviewed_at", itoa(nowUnix()), "id_claim", id)
		d.Update("claim_request", "reviewed_by", c.fromID, "id_claim", id)
		c.sendHTML(c.fromID, sprintf("Admin.claim.approved_msg", r.S("username")), nil)
		c.sendHTML(r.S("id_user"), sprintf("users.claim.approved", r.S("username")), nil)
		return true
	}

	if c.m(`claimno_(\w+)`) {
		id := c.g(1)
		r := d.Select("claim_request", "*", "id_claim", id)
		if r == nil || r.S("status") != "pending" {
			c.sendHTML(c.fromID, T("Admin.claim.missing"), nil)
			return true
		}
		c.savedata(true, "claimid", id)
		c.sendHTML(c.fromID, T("Admin.claim.askreason"), nil)
		c.step("claimreason")
		return true
	}

	if c.stepIs("claimreason") {
		id := c.userdata()["claimid"]
		r := d.Select("claim_request", "*", "id_claim", id)
		if r == nil || r.S("status") != "pending" {
			c.sendHTML(c.fromID, T("Admin.claim.missing"), nil)
			c.step("home")
			return true
		}
		reason := strings.TrimSpace(c.text)
		d.Update("claim_request", "status", "rejected", "id_claim", id)
		d.Update("claim_request", "reason", reason, "id_claim", id)
		d.Update("claim_request", "reviewed_at", itoa(nowUnix()), "id_claim", id)
		d.Update("claim_request", "reviewed_by", c.fromID, "id_claim", id)
		c.sendHTML(c.fromID, sprintf("Admin.claim.rejected_msg", r.S("username")), nil)
		c.sendHTML(r.S("id_user"), sprintf("users.claim.rejected", r.S("username"), reason), nil)
		c.step("home")
		return true
	}

	return false
}
