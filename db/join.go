package db

import (
	"errors"
	"time"
)

// ---------- Joining tables, and moving a dish to another table ----------
//
// Guests push two tables together: one bill goes onto the other (JoinBills).
// A guest moves to another table, or ordered at the wrong one: a dish (or
// some of it) goes to that table's bill (MoveLine). Nothing is taken off a
// bill this way, so waiters may do both. The kitchen's tickets follow, so
// the chef and the "ready" lists show the right table.

var (
	ErrJoinSelf     = errors.New("that's the same table")
	ErrJoinDeposits = errors.New("both tables have an online deposit: pay one bill first")
	ErrJoinBusy     = errors.New("one of the bills is split, part paid, being paid on a phone or has 3 meals running")
)

// joinable reports whether a bill can be joined or have dishes moved
// (callers hold s.mu).
func (s *Store) joinable(b *Bill) bool {
	if b.IsSplit() || len(b.Payments) > 0 || b.QRGuests() || b.Wolt != "" || b.SkippedAt != nil {
		return false
	}
	for _, a := range s.d.PayAttempts {
		if a.BillID == b.ID && a.Pending() {
			return false
		}
	}
	return s.activeRun(b.Table) == nil
}

// moveTickets puts lines' kitchen tickets on another bill. Each line keeps
// its ticket; a ticket shared with dishes that stay is split in two (n of
// the moved line's qty go with it). lineMap: old line ID -> new line ID.
// Callers hold s.mu.
func (s *Store) moveTickets(from, to *Bill, lineMap map[int]int, qty map[int]int) {
	for _, t := range s.d.Tickets {
		if t.BillID != from.ID {
			continue
		}
		var stay, move []TicketItem
		for _, it := range t.Items {
			newID, moved := lineMap[it.LineID]
			if !moved {
				stay = append(stay, it)
				continue
			}
			n := it.Qty
			if q, ok := qty[it.LineID]; ok && q < it.Qty {
				n = q
				rest := it
				rest.Qty -= q
				stay = append(stay, rest)
			}
			m := it
			m.LineID, m.Qty = newID, n
			move = append(move, m)
		}
		switch {
		case len(move) == 0:
		case len(stay) == 0: // the whole ticket goes
			t.Items, t.BillID, t.Table = move, to.ID, to.Table
			s.relink(to, t.ID, move)
		default: // split it: the moved dishes get a ticket of their own
			c := t.clone()
			s.d.NextTicketID++
			c.ID, c.BillID, c.Table, c.Items = s.d.NextTicketID, to.ID, to.Table, move
			t.Items = stay
			s.d.Tickets = append(s.d.Tickets, &c)
			s.relink(to, c.ID, move)
		}
	}
}

// relink points the bill's lines at the ticket that now holds them.
func (s *Store) relink(b *Bill, ticketID int, items []TicketItem) {
	for _, it := range items {
		for i := range b.Lines {
			if b.Lines[i].ID == it.LineID {
				b.Lines[i].TicketID = ticketID
			}
		}
	}
}

// JoinBills puts everything on fromID's bill onto the bill of the table
// intoTable (opened if it has none): guests pushed two tables together. The
// first bill is closed as "joined" (MergedInto), not paid and not voided.
func (s *Store) JoinBills(fromID, intoTable int, now time.Time) (Bill, error) {
	var out Bill
	err := s.changeOrders(func() (bool, error) {
		if !validTable(intoTable) {
			return false, ErrNoTable
		}
		from, err := s.openBillByID(fromID)
		if err != nil {
			return false, err
		}
		if from.Table == intoTable {
			return false, ErrJoinSelf
		}
		into := s.openBillAt(intoTable)
		if into == nil {
			into = s.newBill(intoTable, "", 0, now)
		}
		if !s.joinable(from) || !s.joinable(into) {
			return false, ErrJoinBusy
		}
		if from.DepositCents > 0 && into.DepositCents > 0 {
			return false, ErrJoinDeposits
		}
		lineMap := map[int]int{}
		for _, l := range from.Lines {
			into.NextLineID++
			lineMap[l.ID] = into.NextLineID
			l.ID = into.NextLineID
			into.Lines = append(into.Lines, l)
		}
		s.moveTickets(from, into, lineMap, nil)
		if from.DepositCents > 0 {
			into.BookingRef, into.DepositCents = from.BookingRef, from.DepositCents
		}
		into.Guests += from.Guests
		if into.Waiter == 0 {
			into.Waiter, into.WaiterName = from.Waiter, from.WaiterName
		}
		into.BilledAt = nil
		from.Lines, from.BookingRef, from.DepositCents = nil, "", 0
		from.Voided, from.MergedInto, from.QROpen = true, into.ID, false
		out = into.clone()
		return true, nil
	})
	return out, err
}

// MoveLine moves qty of one dish from a bill to the bill of the table
// toTable (opened if it has none). qty 0 means all of it.
func (s *Store) MoveLine(billID, lineID, qty, toTable int, now time.Time) (Bill, error) {
	var out Bill
	err := s.changeOrders(func() (bool, error) {
		if !validTable(toTable) {
			return false, ErrNoTable
		}
		from, err := s.openBillByID(billID)
		if err != nil {
			return false, err
		}
		if from.Table == toTable {
			return false, ErrJoinSelf
		}
		li := -1
		for i := range from.Lines {
			if from.Lines[i].ID == lineID {
				li = i
			}
		}
		if li < 0 {
			return false, ErrNoLine
		}
		l := from.Lines[li]
		if l.Ask != nil {
			return false, ErrJoinBusy // the manager is looking at it
		}
		if qty <= 0 || qty > l.Qty {
			qty = l.Qty
		}
		to := s.openBillAt(toTable)
		if to == nil {
			to = s.newBill(toTable, "", 0, now)
		}
		if !s.joinable(from) || !s.joinable(to) {
			return false, ErrJoinBusy
		}
		to.NextLineID++
		moved := l
		moved.ID, moved.Qty = to.NextLineID, qty
		to.Lines = append(to.Lines, moved)
		if qty == l.Qty {
			from.Lines = append(from.Lines[:li], from.Lines[li+1:]...)
		} else {
			from.Lines[li].Qty -= qty
		}
		s.moveTickets(from, to, map[int]int{l.ID: moved.ID}, map[int]int{l.ID: qty})
		from.BilledAt, to.BilledAt = nil, nil
		out = to.clone()
		return true, nil
	})
	return out, err
}
