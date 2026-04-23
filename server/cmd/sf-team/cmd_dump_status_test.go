package main

import (
	"context"
	"strings"
	"testing"
)

func TestDumpStatus_RendersReadySections(t *testing.T) {
	d, _ := newTestDB(t)
	ctx := context.Background()

	out, err := d.dumpStatus(ctx, "testdata/items")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "BUG-TEST-002") {
		t.Fatalf("missing BUG-TEST-002 in Ready: %s", out)
	}
	if !strings.Contains(out, "P0") || !strings.Contains(out, "P1") {
		t.Fatalf("missing priority sections: %s", out)
	}
}
