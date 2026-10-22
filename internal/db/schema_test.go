package db

import (
	"os"
	"strings"
	"testing"
)

// schema.go is exercised against the real databases at startup, so there is
// nothing useful to unit-test here with a fake driver. What startup cannot
// catch is a reconciliation step that is absent from the source: that is how
// one legacy database stayed permanently short of two columns the bot reads
// and writes, with every other database fine. These guard the steps.
func schemaSource(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("schema.go")
	if err != nil {
		t.Fatalf("read schema.go: %v", err)
	}
	return string(b)
}

func TestAffiliatesColumnsAreReconciled(t *testing.T) {
	src := schemaSource(t)
	for _, call := range []string{
		`d.addField("affiliates", "affiliatesstatus", "offaffiliates", "VARCHAR(600)", report)`,
		`d.addField("affiliates", "affiliatespercentage", "0", "VARCHAR(600)", report)`,
	} {
		if !strings.Contains(src, call) {
			t.Errorf("a missing affiliates column is no longer reconciled, so a database created by an older\n  installer keeps it missing forever; want: %s", call)
		}
	}
}

func TestUserRefCodeIndexIsReconciled(t *testing.T) {
	if !strings.Contains(schemaSource(t), `d.addIndex("user", "ref_code", "ref_code", report)`) {
		t.Error("the user.ref_code index is only provided by a fresh CREATE TABLE, so an adopted database never gets it")
	}
}

func TestAddFieldAndAddIndexDoNotSwallowFailures(t *testing.T) {
	src := schemaSource(t)
	if !strings.Contains(src, `report("could not add column "`) {
		t.Error("addField drops the ALTER error: a column missing from a legacy database has to be visible in the schema log")
	}
	if !strings.Contains(src, `report("could not add index "`) {
		t.Error("addIndex drops the ALTER error: an index missing from a legacy database has to be visible in the schema log")
	}
}
