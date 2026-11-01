package db

import (
	"testing"

	"ozmo-booking/schedule"
)

// The numbers on a receipt from the old till: 84,00 € of wine and 130,00 € of food.
func TestVATLikeTheOldTill(t *testing.T) {
	rows := VATTable(map[int]int{schedule.VATAlcohol: 8400, schedule.VATFood: 13000})
	want := []VATRow{{Rate: 2550, Net: 6693, VAT: 1707, Gross: 8400}, {Rate: 1350, Net: 11454, VAT: 1546, Gross: 13000}}
	if len(rows) != 2 || rows[0] != want[0] || rows[1] != want[1] {
		t.Fatalf("VAT rows %+v, want %+v", rows, want)
	}
	b := Bill{Lines: []BillLine{
		{Name: "Öppen mat", PriceCents: 3500, Qty: 2, VAT: schedule.VATFood},
		{Name: "Öppen mat", PriceCents: 3000, Qty: 2},
		{Name: "Cuma Malbec, flaska", PriceCents: 4200, Qty: 2, VAT: schedule.VATAlcohol},
	}}
	got := b.VATByRate()
	if got[schedule.VATFood] != 13000 || got[schedule.VATAlcohol] != 8400 {
		t.Fatalf("by rate %v", got)
	}
}
