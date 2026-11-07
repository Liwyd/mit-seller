package bot

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"strings"

	"github.com/Liwyd/mit-seller/internal/panels"
)

const serviceUsernameMax = 32

func randomDigits() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1000000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}

// All generated service names are ASCII. Typed names are validated before this.
func fitUsername(s string) string {
	s = strings.ToLower(s)
	if len(s) > serviceUsernameMax {
		s = s[:serviceUsernameMax]
	}
	return strings.TrimRight(s, "_")
}

func suffixUsername(base string) (string, error) {
	digits, err := randomDigits()
	if err != nil {
		return "", err
	}
	base = fitUsername(base)
	if len(base) > serviceUsernameMax-6 {
		base = base[:serviceUsernameMax-6]
	}
	return base + digits, nil
}

func randomUsername() (string, error) {
	return suffixUsername("user")
}

// Exact, known rejection messages only. A timeout or an unrelated uniqueness
// error must never cause a second create under a different name.
func looksTaken(msg string) bool {
	switch strings.ToLower(strings.TrimSpace(msg)) {
	case "user already exists", "user already exists.", "username already exists",
		"username already exists.", "this username already exists on the panel":
		return true
	}
	return false
}

func chooseAvailableUsername(base string, taken func(string) bool, suffix func(string) (string, error)) (string, error) {
	if !taken(base) {
		return base, nil
	}
	next, err := suffix(base)
	if err != nil {
		return "", err
	}
	if next == base || taken(next) {
		return "", fmt.Errorf("replacement username is already taken")
	}
	return next, nil
}

func (c *Ctx) availableUsername(panel, base string) (string, error) {
	return chooseAvailableUsername(base, func(name string) bool {
		return c.usernameTaken(panel, name)
	}, suffixUsername)
}

// The caller's invoice already contains base: do not let that row self-match.
func (c *Ctx) freeUsername(panel, base string) (string, error) {
	next, err := suffixUsername(base)
	if err != nil {
		return "", err
	}
	if next == base || c.usernameTaken(panel, next) {
		return "", fmt.Errorf("replacement username is already taken")
	}
	return next, nil
}

func retryUsernameCreate(base string, create func(string) panels.Out, replacement func() (string, error), save func(string) error) (string, panels.Out) {
	out := create(base)
	if out.Isset("username") || !looksTaken(out.S("msg")) {
		return base, out
	}
	next, err := replacement()
	if err != nil {
		return base, panels.Out{"msg": err.Error()}
	}
	if err := save(next); err != nil {
		return base, panels.Out{"msg": "Could not persist replacement username"}
	}
	// Once persisted, keep this name even on an ambiguous response. Never retry
	// network errors: the upstream may already have created the account.
	return next, create(next)
}

func (c *Ctx) createService(panel, base string, expire int64, limit float64, trial bool, save func(string) error) (string, panels.Out) {
	return retryUsernameCreate(base, func(name string) panels.Out {
		return c.b.PM.CreateUser(panel, name, expire, limit, trial)
	}, func() (string, error) {
		return c.freeUsername(panel, base)
	}, save)
}

func (c *Ctx) usernameNotice(to, before, after string) {
	if before != after {
		c.sendHTML(to, sprintf("users.buy.usernametaken", after), nil)
	}
}

// Persist all legacy username-based references together BEFORE retrying create.
// If the panel response is lost, the payment still points at the name attempted.
func (b *Bot) renamePendingUsername(invoiceID, owner, before, after string) error {
	tx, err := b.DB.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.Exec("UPDATE invoice SET username = ? WHERE id_invoice = ? AND id_user = ? AND username = ? AND Status = 'unpaid'", after, invoiceID, owner, before)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil || n != 1 {
		return fmt.Errorf("pending invoice changed while renaming")
	}
	if _, err = tx.Exec("UPDATE Payment_report SET invoice = ? WHERE id_user = ? AND invoice = ?", "getconfigafterpay|"+after, owner, "getconfigafterpay|"+before); err != nil {
		return err
	}
	if _, err = tx.Exec("UPDATE user SET Processing_value_one = ? WHERE id = ? AND Processing_value_tow = 'getconfigafterpay' AND Processing_value_one = ?", after, owner, before); err != nil {
		return err
	}
	return tx.Commit()
}
