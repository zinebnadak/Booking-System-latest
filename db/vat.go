package db

import (
	"sort"

	"ozmo-booking/schedule"
)

// VAT on receipts and in the day report, the way the old till printed it:
//
//	Moms %   Utan moms   Moms    Med moms
//	25,50       66,93    17,07      84,00
//	13,50      114,54    15,46     130,00
//
// Prices include VAT, so the VAT is worked out backwards from each rate's
// total: VAT = total × rate / (100 % + rate), rounded to the cent.

// VATRow is one VAT rate's part of a total. Rate is in hundredths of a
// percent (2550 = 25,5 %); the amounts are cents.
type VATRow struct {
	Rate  int
	Net   int // without VAT
	VAT   int
	Gross int // with VAT
}

// vatIn is the VAT inside a price that includes it, rounded half up.
func vatIn(gross, rate int) int {
	if gross < 0 {
		return -vatIn(-gross, rate)
	}
	return (gross*rate*2 + (10000 + rate)) / (2 * (10000 + rate))
}

// VATTable turns totals per rate into receipt rows, highest rate first.
func VATTable(byRate map[int]int) []VATRow {
	var out []VATRow
	for rate, gross := range byRate {
		if gross == 0 {
			continue
		}
		v := vatIn(gross, rate)
		out = append(out, VATRow{Rate: rate, Net: gross - v, VAT: v, Gross: gross})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Rate > out[j].Rate })
	return out
}

// VATByRate adds up a bill's lines per VAT rate (what was sold: the deposit
// paid online and tips don't change it).
func (b Bill) VATByRate() map[int]int {
	out := map[int]int{}
	for _, l := range b.Lines {
		out[l.VATRate()] += l.Total()
	}
	return out
}

// ShareVATByRate is VATByRate for one guest's part of a split bill.
func ShareVATByRate(lines []ShareLine) map[int]int {
	out := map[int]int{}
	for _, l := range lines {
		rate := l.VAT
		if rate == 0 {
			rate = schedule.VATFood
		}
		out[rate] += l.Cents
	}
	return out
}
