package postgres

import "testing"

func TestQuoteIdent(t *testing.T) {
	if got := QuoteIdent(`odd"name`); got != `"odd""name"` {
		t.Fatalf("QuoteIdent() = %q", got)
	}
}

func TestSplitTable(t *testing.T) {
	schema, table, ok := SplitTable("public.orders")
	if !ok || schema != "public" || table != "orders" {
		t.Fatalf("SplitTable() = %q %q %v", schema, table, ok)
	}
	if _, _, ok := SplitTable("orders"); ok {
		t.Fatal("SplitTable accepted unqualified table")
	}
}
