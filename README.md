# E-Meal Kitchen: booking system with table buttons, table bills and a kitchen screen

> This is the E-Meal Kitchen copy of the booking system: the same program,
> with E-Meal Kitchen's name, contact details (Ålandsvägen 40, Mariehamn ·
> 0400 796 789 · info@emeal.ax) and colours: dark green-black `#171d1d`,
> brown `#612b18` and gold `#d2aa45`, Cinzel headings and Roboto text. The
> menu is still the demo menu. It runs on port 8080 (`go run .`) and keeps its
> data in `data/emeal.json`; `phone-test.command` uses port 8098.

Table bookings and takeaway orders with a Stripe deposit, confirmation emails,
a manager dashboard with a calendar, and in the restaurant itself:

- **Orders at the tables:** staff put dishes and drinks on a table's bill during
  the meal, and the guests pay at the end.
- **A kitchen screen for the chef:** every time food is ordered, it pops up with a
  chime. Drinks never do.
- **A real button on every table** for the **"3 meals" weekend deal**: guests press
  it when they finish a meal, and staff see "Table 4 – ready for meal 2".
- **Order and pay from the phone:** guests scan the QR code on their table, order
  for themselves, and pay their part, or the whole table, with Apple Pay,
  Google Pay or a card.
- **Feedback after the visit**, private to the restaurant, with an invitation to
  review it on Google.
- **A login for every waiter**, a list of who did what with money, and an
  end-of-day email for the owner.
- **Everything the old till (Restolution) did:** the three rooms with their 50
  tables, the real menu with VAT 13,5 % / 25,5 % on receipts, "Vänta varmt" and
  "Varma fram", discounts, After Work set menus, cocktail-hour prices, lunch cards,
  paying in parts, gift cards and lunch booklets, a PIN keypad for waiters and a
  clock card (section 10).

> **E-Meal Kitchen is a working name.** The name is one setting (`RESTAURANT_NAME` in
> `.env`). The menu and tables are copied from the old till and printed menus;
> opening hours, seats per table and some prices are still placeholders until
> the restaurant confirms them (`schedule/schedule.go` and the Menu page).

Built with Go's standard library only: **no packages to install**.

Every page is in **Swedish and English**, for guests and staff, with a switch at the top (SV / EN on the staff pages). The choice is remembered on that device. Guest words live in `handlers/i18n.go`, staff words in `handlers/staff_i18n.go`: change a word there and it changes everywhere. Emails to the manager are in English.

---

## 1. Run it on your Mac (demo mode)

```bash
cd emeal-booking
go run .
```

Open **http://localhost:8080**. Staff log in at **http://localhost:8080/login**:
- **Manager:** `admin` / `admin`. Sees everything: orders, bookings, menu, staff, and the money pages and charts.
  (There used to be a separate head manager; that's now the same as the manager.)
- **Waiters:** the manager adds them on **Staff** (section 12).

You stay logged in for 14 days on that device, or until you press **Log out**.

In demo mode:
- **Payments are simulated.** You get a fake "Pay" page instead of Stripe.
- **Emails are printed in the terminal** instead of being sent.

So you can click through everything before any keys exist.

Want to show the restaurant a busy evening during the day, or try the weekend
deal on a Wednesday? Pretend it's another moment (demo mode only):

```bash
DEMO_CLOCK=2026-10-31T18:05 go run .
```

That's a Saturday evening, so **3 meals** can be started on the Tables screen.

Run the tests: `go test ./...`

---

## 2. What's in the project

| Path | What it does |
|---|---|
| `main.go` | Reads settings from `.env`, starts the web server |
| `schedule/` | **The restaurant's rules**: opening hours, slots, 2-hour bookings, deposits. Change rules here. |
| `db/` | Saves bookings, menu, closed days, table buttons, bills and kitchen tickets in `data/emeal.json`. Prevents double booking. `seed.go` is the restaurant's menu, `tables.go` the buttons and the 3 meals deal, `orders.go` the bills and the kitchen, `courses.go` "Vänta varmt" and kitchen messages, `vat.go` VAT, `payments.go` ways to pay and paying in parts, `vouchers.go` gift cards and lunch booklets, `shifts.go` the clock card, `guests.go` QR guests and the payment lock, `phoneorders.go` the waiter check, `reviews.go` feedback, `staffaccounts.go` waiters' logins, PINs and the activity list. |
| `payments/` | Talks to Stripe: payment page, refunds, and checking webhooks are real |
| `mailer/` | Sends emails via SMTP |
| `handlers/` | Every page: booking, takeaway, status page, webhook, manager dashboard. `manage.go` (guests changing and cancelling, waiting list), `staff.go` (phone bookings, walk-ins), `tables.go` (table buttons, the live Tables screen), `orders.go` (bills at the tables, the kitchen screen), `guests.go` (ordering and paying from the table's QR code), `photos.go` (dish photos), `reviews.go` (feedback), `staffpages.go` (waiters and activity), `summary.go` (end-of-day email), `rooms.go` (rooms on the staff screens), `paying.go` (ways to pay), `giftcards.go`, `till.go` (PIN keypad, clock card), `hours.go`, `jobs.go` (reminders, waiting list, privacy clean-up every 5 minutes) |
| `templates/` | The HTML pages |
| `static/style.css` | The design |
| `button/` | **The code for the table button itself**, the parts list and how to build one |

### The rules (placeholders until the owner confirms them)
- **Three rooms and 50 tables, numbered like on the old till:** Utrymme 1 (1–18,
  151, 161), Utrymme 2 (19–31) and Terrassen (50–66). `TablePlan` in
  `schedule/schedule.go`. **The seats per table are a guess from the shapes on the
  old table map: check them.**
- One online booking takes one table, for 1–6 guests: **the smallest free table
  with enough seats**, so a couple doesn't take a table for eight. Any table can be
  booked online as long as it's free; a whole room can be closed for online
  bookings on **Closed days** (the terrace in winter). Bigger groups call.
- Bookings last 2 hours and start every 30 minutes. The last start is 2 hours before closing.
- Hours: Mon–Thu 10:30–21:00, Fri 10:30–22:00, Sat 16:00–22:00, Sun 12:00–20:00.
- **3 meals** runs on Saturday and Sunday (`MealDealDays` in `schedule/schedule.go`).
- Deposit: **10 €** for 1–2 guests, **20 €** for 3–6, taken off the bill. Refunded if cancelled 24h+ before.
- Takeaway is food only. The deposit is **25 %** of the total and non-refundable. Pickup is every 15 minutes while the kitchen is open, at least 30 minutes after ordering.

### Assumptions (easy to change in `schedule/schedule.go`)
- Tables must be booked at least **1 hour** ahead, and at most **60 days** ahead.
- Takeaway can be ordered up to **7 days** ahead. The first pickup is 30 minutes after opening.
- An unpaid booking holds the table for **35 minutes**, then it's released.

---

## 3. How a booking works

1. The guest picks a date, guests and a time, then fills in their details.
2. The booking is saved as **pending** and the table is held for 35 minutes.
3. The guest is sent to the **Stripe payment page**. The payment link is also emailed to them.
4. Stripe tells our server it's paid (the **webhook**), and the booking becomes **confirmed**.
5. The guest gets a confirmation email, plus a receipt from Stripe. The manager gets an email.
6. The booking appears in the dashboard and in the manager's phone calendar.

If the guest never pays, the hold runs out and the table is free again.

### What guests can do themselves

The link in the confirmation email (`/b/<code>`) is the guest's own booking page. From there they can:

- **Change** the day, time or number of guests, until 24 hours before. The deposit stays the same, so they can switch between 1–2 guests or between 3–6 guests. Bigger changes: they call.
- **Cancel.** 24 hours or more before, the deposit is refunded through Stripe automatically. Later, the deposit is kept, and the page says so before they confirm. Takeaway can be cancelled too, but the deposit is always kept.
- **Add it to their calendar.**

The manager gets an email for every change and cancellation.

### Reminders

The day before (less than 24 hours, but more than 2 hours, before the booking), the guest gets a reminder email with the link to cancel. Bookings made in the last 12 hours don't get one; they just got their confirmation. Reminders are the best-known way to cut no-shows.

### Waiting list

When some or all times on a day are full, guests can join the waiting list (day, guests, preferred time). As soon as a table frees up (a cancellation, a change, an unpaid booking running out), everyone waiting for that day gets an email with the free times and a booking link. The first to book gets the table. Guests can leave the list from the link in their email. Staff see the list on the day page.

### Terms and privacy

`/terms` and `/privacy` explain the deposit, cancellation, no-show and takeaway rules, and how guest details are handled. The pay buttons link to them. **Have the owner read both pages once** and change anything that doesn't match how they work. The text is in `templates/legal.html`.

Guests' names, emails, phone numbers and notes are removed automatically from bookings older than `DATA_RETENTION_DAYS` (365 days by default). Amounts, dates and dishes stay, so the reports still add up.

---

## 4. Connect Stripe (test mode first)

1. Ask the owner to invite you in Stripe: **Settings → Team → Developer role**.
2. In Stripe, switch on **Test mode**. Copy the secret key (`sk_test_...`) from **Developers → API keys**.
3. Make your settings file and fill it in:
   ```bash
   cp .env.example .env
   ```
   Set `STRIPE_SECRET_KEY`, `ADMIN_PASSWORD` and `MANAGER_EMAIL`.
4. Forward webhooks to your Mac with the Stripe CLI:
   ```bash
   brew install stripe/stripe-cli/stripe
   stripe login
   stripe listen --forward-to localhost:8080/stripe/webhook
   ```
   It prints a `whsec_...` secret. Put it in `.env` as `STRIPE_WEBHOOK_SECRET`.
5. Run `go run .`, book a table, and pay with the test card **4242 4242 4242 4242** (any future date, any CVC).

---

## 5. Connect email

Any provider with SMTP works. Fill in the `SMTP_*` and `MAIL_FROM` settings in `.env`.

- **Gmail:** host `smtp.gmail.com`, port `587`. You need an **App password** (Google account → Security → App passwords).
- **Best for a business:** an email on the restaurant's own domain, through a service like Brevo, Resend or Mailgun. These deliver more reliably, so emails are less likely to land in spam.

---

## 6. Go live (checklist)

1. **Hosting:** it must keep the `data/` folder between restarts. Options:
   - a small VPS (e.g. Hetzner, around 4 €/month)
   - Fly.io or Railway with a **volume** mounted at `/app/data`

   Build with `go build`, then upload the single program file. The pages are built into it.
2. Set `ADMIN_PASSWORD` and `OWNER_PASSWORD`: at least 12 characters each, and different (`openssl rand -base64 18` makes one). The program won't start on a public address without them.
   If the host puts a proxy in front (Caddy, nginx, Render, Fly.io), also set `TRUST_PROXY=1`.
3. Put a domain in front of it with **HTTPS**, e.g. `book.ozmo.ax`. Set `BASE_URL` to that address.
4. In Stripe live mode:
   - Copy the `sk_live_...` key.
   - Go to **Developers → Webhooks → Add endpoint**: `https://<your-domain>/stripe/webhook`.
   - Select the events `checkout.session.completed`, `checkout.session.async_payment_succeeded` and `checkout.session.expired`.
   - Copy its `whsec_...` secret.
5. Turn on receipts: Stripe **Settings → Customer emails → Successful payments**.
6. Set `CALENDAR_TOKEN` (any long random text). The **Calendar** page then shows a link the manager adds to their phone once.
7. **Back up** the whole `data/` folder every day: `ozmo.json` holds all bookings, bills, button codes, feedback and waiters' logins, and `photos/` the dish photos.
8. Do one real booking with a real card and refund it from the dashboard.
9. Print the table QR stickers and the review sticker from the real address (**Orders → Table QR codes**), and try "Pay for the whole table" once with a real card, then refund it in Stripe.
10. Set `GOOGLE_REVIEW_URL` (section 11) and, if the end-of-day email should go somewhere else than `MANAGER_EMAIL`, `REPORT_EMAIL` (section 13).
11. Add a login for each waiter on **Staff**, so nobody shares the manager password.
12. Print **Orders → If the internet goes down** and keep it by the till with an order pad. Check once that the card terminal works with the Wi-Fi off.
13. Set `RESTAURANT_ADDRESS`, `RESTAURANT_PHONE` and `BUSINESS_ID` (the FO-nummer on the old receipts): they're printed on every receipt.
14. With the owner: the seats of each table, the prices marked **Price missing** on the Menu page, the cocktail-hour times and prices, and the After Work split (section 10).
15. Make the shared screen a till (**Staff → Make this device a till**) and give each waiter a PIN.

### When the internet goes down

The tablets, the kitchen screen, the table buttons and the QR codes all need the
internet. If it goes down, the restaurant keeps going on paper:

- **Every staff screen warns at once**, in red at the top: "No internet. Switch
  to paper.", with the short version of the plan. It goes away by itself when the
  connection is back.
- **The full plan** is on **Orders → If the internet goes down**
  (`/admin/offline`), made to be printed: tell everyone, check with a phone on
  mobile data (if only the Wi-Fi is down, share the phone's internet with the
  kitchen tablet), take orders on paper, the kitchen cooks from the paper, take
  payments at the terminal or in cash, and walk round the tables because the
  buttons can't light up the screen.
- **When it's back:** put the paper orders on the bills and mark how they were
  paid, so the end-of-day email and the activity list are right.
- The QR stickers say **"Doesn't open? Ask your waiter"**, so guests aren't left
  stuck.
- There are two blank lines at the bottom of the printout for the internet
  provider's number and who to call about the system.

### How the site protects guests and staff

- **Payments:** card details only ever go to Stripe. The site never sees them, and a booking only counts as paid when Stripe's signed message says so.
- **Staff logins:** passwords are checked in a way that doesn't leak them through timing. Waiters' passwords are stored only as a slow, salted fingerprint (PBKDF2-SHA256), and a wrong username takes as long as a wrong password. A new password or removing a waiter logs them out everywhere at once. After 10 wrong passwords from one address, that address must wait 15 minutes. The login lasts 14 days in a signed cookie that scripts can't read.
- **Guest links:** every booking link has a random 32-character code that can't be guessed. The pages with guests' details are never stored by the browser or by proxies.
- **Too many tries:** one address can make 6 bookings or orders per 10 minutes. One guest (same email or phone) can have at most 3 unpaid bookings waiting. So nobody can block every table without paying, or use the site to send lots of emails to someone.
- **Browser rules:** every page tells the browser to run only the site's own scripts, never to show the site inside another site, and to send forms only here or to Stripe.
- **Fonts and privacy:** the fonts are served from this site, so nothing about visitors goes to Google or anyone else.
- **Spreadsheet export:** names and emails that look like formulas get a `'` in front, so they can't run when the file is opened in Excel.
- **Data:** guests' details are removed after the visit: after 365 days by default (`DATA_RETENTION_DAYS`). The data file is readable only by the program's user.
- **QR orders at the table:** a table's sticker only works while the table has guests, and its code can be replaced if a photo gets around. Each phone gets its own random 32-character key; keys never appear in the server's logs and guest pages are never stored or indexed. Prices always come from the menu, never from the phone. Paying locks the parts being paid in one step on the server, so the same part can't be paid twice, and a payment page left open is cancelled at Stripe before the lock lifts.
- **Dish photos:** only real JPEG or PNG images are accepted, they're re-saved as new files (nothing hidden inside them survives, including the location), and they're served only by their random file names.
- **Feedback:** no login needed and no data sent to Google. A hidden trap field stops simple spam bots, and one address can send 6 per hour. Guests' emails in feedback are removed with the rest of the old data.
- **Table buttons:** each button has its own random 32-character code; the site compares codes in a way that doesn't leak them through timing, never accepts a code in the address (addresses end up in logs), and stops listening to an address after 20 wrong codes in 10 minutes. A right code always works, so a guest sending wrong codes over the restaurant's Wi-Fi can't block the real buttons. Staff taps carry the meal number, so two waiters tapping "Meal 2 served" at once can't skip a meal.

---

## The kitchen's own login

On **Staff**, the manager adds a login and picks **This login is for: The
kitchen (chef)** (or switches an existing one with "Make it the kitchen's
login"). The chef logs in with that username and password and lands on the
kitchen screen; that login opens only the kitchen screen and the stock.
Waiters don't see the kitchen screen at all: they see on Orders when food is
ready (the table turns green), tap **Served** there, and tap **Brought** for
drinks. The manager sees everything.

## Switched off for now

Three features are built but switched off, so the staff screens don't show
them. Nothing was deleted; set them to `1` in `.env` to turn them back on:
- `TABLE_BUTTONS=1`: the call button on each table (and its page under the
  money pages). Off: with 3 meals, the waiter sends the next meal from the
  table's page.
- `TABLE_QR=1`: guests order and pay from their phones with the table's QR
  code. Off: there's one QR code, for feedback (Orders → **Feedback QR
  code**, print it), and an old table sticker leads to the feedback page.
- `TABLE_DEPOSIT=1`: guests pay a deposit to book a table online. Off:
  booking a table is free and confirmed straight away (takeaway still
  pays 25 % up front).

## Nothing comes off a bill without the manager

Against "take the cash and delete the order":
- **Waiters can only add and take payment.** Once something is on a bill, a
  waiter can't take it off, lower how many or the price, give a discount or
  void the bill. (Before tapping "Add to bill" they can change what they
  picked as much as they like.)
- **Added by mistake?** Open the dish and tap **Ask the manager to take it
  off**, with how many and why. It stays on the bill. The manager sees it at
  the top of Orders (and on the table's page) and taps **Take off** or **No,
  keep it**.
- **Skip for now:** guests left without paying, or new guests need the table.
  The bill leaves the table, still open and unpaid, and waits under
  **Skipped, not paid yet** on Orders. The table is free. If the guests come
  back, anyone can take the payment there. The manager can put it back on a
  table or **Write off** as never paid, with why.
- **Undo for one minute:** right after "Add to bill" the line has an
  **Undo** button for a minute (if the chef hasn't finished it). It shows on
  the charts as "Undone by the waiter".
- **Manager's PIN:** the manager sets a PIN on the Staff page. Standing next
  to a waiter, they type it in the dish's window on the waiter's iPad or
  phone and it comes off at once, recorded as "Manager (PIN)", asked by that
  waiter. Wrong PINs are slowed down like wrong passwords.
- **Red number in the top bar:** the manager sees how many questions and
  skipped bills wait, next to Orders, on every page.
- **Charts → Taken off bills:** everything taken off, for the month or year:
  how many times, how many dishes and drinks, what it was worth (taken off,
  voided bills, never paid), per waiter, and every one with when, table,
  what, why, who asked and who took it off. Bills skipped and still unpaid
  are shown too. A waiter with 3 or more taken off or skipped in the last 7
  days is marked "Many this week" (their own one-minute undos don't count).

## Waiters: quicker on the floor

- **Whose table:** the waiter who opens a bill looks after that table; its
  tile on Orders has their name in small letters in the bottom corner. On the
  table's page: **Take this table**, or **Change** to hand it to someone else.
- **Needs a waiter:** a table nobody has (guests opened it with the QR code)
  is listed at the top of Orders on every screen, with a chime, until a
  waiter taps **Take it** (or the manager picks a waiter and taps **Give**).
- **All tables / My tables:** Orders shows every table; a waiter can switch to
  only their own (and the free ones). The choice is remembered on the device.
- **Who served the most tables:** on the Staff page (manager), tables paid
  today and this month per waiter, with what the guests paid.
- **Card · 40,00 €** on a tile once everything is served or the bill was
  shown: pays the whole bill by card from Orders, after a Yes.
- **Most ordered:** the 8 dishes and drinks that sold most in the last 30
  days are big buttons on top of the menu. One tap adds one.
- **Quick notes for the chef:** under "Note or other price" on a dish: No
  onions, Gluten free, Lactose free, No nuts, Extra sauce, Sauce on the side,
  Well done, Medium. Tap to add or take away (in `handlers/quick.go`).

## 7. Table buttons and the "3 meals" deal

On Saturdays and Sundays guests can take **3 meals**: they get one meal, and when
they've finished it they press the button on their table. Staff take the plates
and bring the next one.

### What staff see: everything on Orders (`/admin/orders`)

There's no separate Tables screen any more: the button, the QR code's "call
the waiter", 3 meals and bookings are all on **Orders**, on a phone, an iPad
or a tablet by the kitchen pass. Old links to `/admin/tables` open Orders.

- Each table's tile shows its bill, who is seated, **Booked 19:00 · Andersson ·
  4 guests** for bookings still to come today, and the 3 meals state with dots.
- Tap a table: at the top of its page are the 3 meals and call buttons below,
  then the bill and the order form.
- One tile per table. It **lights up** (orange border) the moment the button is pressed:
  - orange **"Ready for meal 2"** (or 3) during the deal
  - orange **"Finished all meals"** after the last one
  - blue **"Wants a waiter"** when no deal is running (the buttons are useful every day)
- A list at the top shows the waiting tables, the longest wait first, with minutes.
- **Sound:** tap **Turn on sound** once (browsers need one tap before a page may make noise). A new press then plays a chime and vibrates the phone, and while a table is still waiting it chimes again every 2 minutes. The screen also stays awake.
- **Starting 3 meals** (on the table's page): type how many guests and, if
  they want, which dish is meal 1, 2 and 3 (e.g. rice first, then fish, then
  meat). Tick **Save this order as** to keep it; next time pick it under
  **Saved order**. The price (20 € per guest to start with) goes on the bill as
  "3 rätter". The manager changes it on the Menu page, under **3 rätter**.
  While it runs, the table shows the dishes in order and "Ready for meal 2 · Fish".
- **In the kitchen:** meal 1 goes to the chef's screen when 3 meals starts
  ("3 rätter · rätt 1 av 3", with the number of guests). When the chef taps
  Done, a card stays under **3 rätter · mellan rätterna**: "2 rätter kvar",
  which dishes, and that the guests haven't asked for meal 2 yet. Meal 2 comes
  onto the screen with a chime when the guests press the button (or tap "Ready
  for the next meal" on their phone), or when a waiter taps **Send meal 2 to
  the kitchen** on the table's page. **Not yet, still eating** takes it back
  (crossed out for the chef). Served in the kitchen counts as served at the
  table. If the guests leave early, **Stop 3 meals** (or paying the bill)
  takes the table off the kitchen screen.
- Taps (on the table's page):
  - **Start 3 meals:** meal 1 goes to the kitchen
  - **Meal 2 served** / **Meal 3 served:** the next meal is on the table and the plates are gone. Also works if the guests didn't press.
  - **Table cleared:** after the last meal (or to stop the deal early: **Stop 3 meals**)
  - **Not yet, still eating:** for a press that wasn't meant (children love buttons)
  - **Done:** for "wants a waiter"
- At the bottom: today's figures, **average time to eat a meal** and **average wait after the button**: how fast the kitchen and floor are reacting.
- If a button's battery is low, or a button hasn't been heard from in 2 days, a note says which table.

Two waiters can use it at once: every screen updates within a second, and an
out-of-date tap ("Meal 2 served" twice) is refused instead of skipping a meal.

### Try it without any buttons

In demo mode, open **http://localhost:8080/demo/buttons** on your phone (or a
second window). It shows six big buttons that act exactly like the real ones:
press **Table 4**, and table 4 lights up on the Tables screen. (To reach your
Mac from your phone, see "Open it on your phone" below.)

### The real buttons

Each button is a tiny Wi-Fi board (Seeed XIAO ESP32C3) with a push button, an LED
and a small rechargeable battery, in a box on the table. It sleeps until pressed,
then sends its table's secret code to the site and goes back to sleep. Once a day
it wakes to report its battery.

**How to build and set one up: [`button/README.md`](button/README.md).**

The head manager sees each button's secret code, last signal and battery on
**Table buttons** (`/owner/buttons`), and can make a new code if a button is lost.

Any other button that can send a web request works too (`POST /api/press` with
the header `Authorization: Bearer <code>`).

### Open it on your phone (same Wi-Fi as your Mac)

```bash
echo "http://$(ipconfig getifaddr en0):8080"
```

prints your Mac's address, e.g. `http://192.168.1.23:8080`. Open that on your
phone. If the Mac asks whether to accept incoming connections, click **Allow**.

---

## 8. Orders at the tables and the kitchen screen

Guests order at the table and pay when they leave. Everything they order during
the meal goes on the same bill.

### Taking an order (`/admin/orders`)

1. **Orders** shows every table: what its bill comes to so far, and where it is:
   **With the chef** → **Food ready · pick up** (green) → **Served · waiting to pay**
   (blue) → **Bill shown · waiting for payment**.
2. Tap a table. Tap **+ Add** on dishes and drinks (− / + for more). Under each dish,
   **Note or other price** lets you write a note for the chef ("no onions") or charge
   a different price. **Something not on the menu** is for anything else.
3. **Add to bill.** Food goes to the chef at once; drinks only go on the bill.
4. During the meal: open the table again and add more ("a soda, please"). Same bill.
   After **Add to bill**, a big **Back to orders** button takes you home; **Home** at the
   top of every table does the same. Orders is the staff's home page: logging in, the
   restaurant's name at the top and the first menu link all open it.
5. On the bill, **Change** fixes a price or how many drinks, or takes something off.
   Food the chef already has can only change price; take it off and add it again
   to change it, and the chef sees it crossed out.
6. **Show the bill** opens a clean bill to show the guests or **print**.
7. **Paid at the terminal** / **Paid in cash** closes it. The table is free for the next guests.

If the guests booked online, their **deposit comes off the bill by itself**.
**Discount, move or void** gives a discount on the whole bill, moves it to another
table (guests moved) or throws it away (opened by mistake). **Paid today** at the
bottom of Orders adds up the day's bills per way of paying. Everything else the old
till did (courses, discounts, lunch cards, gift cards, receipts with VAT) is in
section 10.

### Splitting the bill

**Waiter and phones at the same table.** The waitress can type in an order for
a guest, and he can also scan the QR code and order a drink himself. It's all
one bill: his drink appears on the table's bill as his (**Guest 1 · Erik**),
the waiters see it under **Drinks to bring** with a chime, and stock counts it
(if it's out, his phone can't order it). To take one payment for everything,
the table's page shows **Still to pay at this table** with **All at the
terminal** and **All in cash**: what nobody had goes on the first guest who
hasn't paid (or on a new guest if everyone else paid on their phones), and the
table is free.

When guests want to pay for themselves, everything happens in one trip to the table:

1. On the table's bill tap **Split the bill**, then how many are paying (2–12).
2. **Who had what?** Under each dish and drink, tap the guest number(s). Tap two or
   more to share something: it's divided equally (to the cent: the parts always add
   up to the bill). **Split evenly** divides everything. "3× Läsk" on one line?
   **Make 3 lines** and give each to whoever had it.
3. **Each guest pays:** every guest's part and what they had. Bring the card terminal
   and tap **Terminal** or **Cash** under each guest as they pay. When the last guest
   pays, the table is free.
4. **All receipts (print / QR):** every guest's receipt in one go, each with only what
   they had, labelled "Table 5 · Bill 2 · Guest 1 of 4". **Print all** gives one slip
   per guest, or each guest **scans their QR code** and gets the receipt on their own
   phone (no login, the code in the link is long and random so nobody can guess
   someone else's). The whole bill's receipt has a QR code too.

- **Someone was missed?** **+ Add a guest** works any time, even after others paid.
- **One pays for several, or the rest pay in one go:** under **One payment for several
  guests**, tick who's included (everyone left is ticked to start with), and one
  payment on the terminal covers them all. Their receipts say "Guest 1 paid this
  for you", and the payer's says "You also paid for guests 2 and 3: 59,70 € in total".

A guest who already paid can't have their part changed by mistake; **Undo** takes
back a payment tapped by mistake (a payment for several is taken back for all of them). The online deposit (if they booked) comes off
guest 1's part: the one who booked paid it.

In the table's history the bill keeps its full amount, with each guest's part
and how they paid underneath. **Paid today** counts every part as card or cash.

**Testing the QR codes on your Mac:** open the staff pages on your phone through the
Mac's address (`http://192.168.x.x:8080`, see section 7), not `localhost`. The QR
codes then point at that address, so a second phone on the same Wi-Fi can open them.
Live, they point at `BASE_URL`.

### Every table's history

Nothing is ever thrown away: every bill stays, paid or not. **History** on a table
(or **Table history** at the bottom of Orders) shows all its bills, newest day
first, 14 days per page:

- **Table 5 · Saturday 31 October**: **Bill 1** 18:05–19:20, paid at the terminal, 15,00 €;
  **Bill 2** 19:45–20:30, paid in cash, 48,00 €; **Bill 3** open now.
- Tap a bill to see the full order: every dish and drink, notes, and where a price
  was changed, the menu price next to it.
- **Show the receipt** opens it again exactly as the guests saw it, numbered
  ("Bill 2 · #57"), to show or print a copy.
- Voided bills are kept too, crossed out, so nothing disappears quietly.

The number after **#** is unique for every bill, handy when matching a payment on
the card terminal's day report.

### Stock counts every way of ordering

A counted dish (or its ingredients) goes down the moment it's ordered, whichever
way: a waiter on a phone or iPad, a guest with the table's QR code, or online
for takeaway (counted when it's paid; an order is stopped before paying if it's
no longer there, and a cancelled takeaway goes back into stock). On the **Stock**
page, **Food / Drinks / Both** shows only that part, and the search box finds a
dish or drink within it. The choice is remembered on each phone or iPad.

### The chef's screen (`/admin/kitchen`)

Open it on a screen or tablet in the kitchen, logged in as the manager, and tap
**Turn on sound** once.

- A new card with a chime every time food is ordered: **Table 5 · 18:42**,
  **2× E-Meal-burgare**, **→ no onions**. No prices, no drinks.
- Cards turn orange after 15 minutes and red after 25.
- The chef taps **Done**. The food is now **Ready at the pass**, and the waiters'
  Orders and Tables screens show **Food ready to pick up: Table 5 · 1× E-Meal-burgare
  (Erik)** with a chime, again every 2 minutes, red after 4 minutes.
- When a waiter takes it, the chef (or the waiter, on their own screen) taps
  **Served**. The table then shows **Served · waiting to pay**.
- **Not done yet** sends it back to cooking. **Undo** under "Served and done"
  puts it back at the pass.

### Takeaway orders on the chef's screen

As soon as a takeaway order's deposit is paid, it appears on the kitchen screen
(in blue, with a chime): **Takeaway #12 · pickup 18:00**, the dishes, and
**"Ordered online. Erik picks it up at 18:00."** plus the customer's note.

- The chef taps **Seen · remind me at 17:00**. That's the sign he saw it. It
  moves to **Takeaway coming up** at the bottom of the screen.
- **An hour before pickup** it's back on the screen with a chime: **Start now ·
  pickup in 60 min**. It turns orange 30 minutes before pickup and red 10 minutes
  before. The chef taps **Done** when it's ready.
- An order for a later day stays in "coming up" until that day.
- Cancelled: it's crossed out. Picked up (staff tap **Picked up**): it's gone.

The hour is one setting: `TakeawayKitchenHeadsUp` in `schedule/schedule.go`.

### Sold out (the chef taps it)

At the bottom of the kitchen screen, **Sold out?** lists every dish. When one
runs out, the chef taps it:

- Guests can't order it any more: it disappears from the menu on their phones
  at the table and on the takeaway page. Someone who already had it in their
  order gets "Sorry, something you picked just sold out" and nothing is ordered.
- Tap it again if more turns up.
- It's back by itself tomorrow, so nobody has to remember to switch it on.
- The **Menu** page shows a **Sold out today** badge on it too.

### Stock: what's run out (`/admin/stock`)

So a waiter can say "sorry, that's finished" **before** the guest chooses it,
not after walking to the bar to find the shelf empty. Every staff member can
use it: waiters, the kitchen (there's a **Stock** button on the kitchen
screen) and the manager.

For each drink and dish on the menu:

- **Out of stock**: nobody can order it until someone taps **Back in stock**.
  Unlike the chef's "sold out today", it does not come back by itself: use it
  for a drink that's finished in the storeroom.
- **Left**: type how many there are (say 6 bottles of Pinot Grigio) and tap
  **Save**. It counts down by itself as it's ordered, by waiters or on guests'
  phones. At 3 or fewer it shows as **running low**; at 0 it's out of stock.
  Clear the box and tap **Save** to stop counting.

What everyone sees:

- **Waiters:** a red line at the top of **Tables**, **Orders** and the order
  page: "Out of stock: … · Running low: Pinot Grigio (2 left)". On the order
  form, what's out is crossed out with nothing to tap, and counted items say
  how many are left. Searching the menu finds it too, so typing "pinot" while
  the guest is talking shows at once that it's finished.
- **Two waiters, one last bottle:** whoever sends first gets it; the other sees
  "Only 1 left of …. Nothing was added." Nothing is half-added.
- **Guests:** it's gone from the phone menu and the takeaway page.
- **Taken off the bill** (typed by mistake): it's counted back, unless the
  kitchen already marked it ready or a waiter marked the drink brought.
  A rejected phone order is counted back too. **Voiding** a bill doesn't count
  anything back (usually the guests had it): take lines off first if they never
  got them.
- **Activity list:** who marked what out, back, or counted, and when.

Takeaway orders hide what's out, but don't count down (they're often for later
in the day or another day).

### Ingredients: spaghetti, cheese… (Stock page)

Under **Ingredients** on the Stock page, count what dishes are made of, each in
its own unit: Spaghetti 5000 g, Ost 2000 g, Lax 20 st. **Warn at** says when it
counts as running low (e.g. 1000 g).

On the **Menu** page, each dish has **Uses from stock (one portion)**: pick the
ingredient and how much one portion takes (Carbonara: 120 g spaghetti, 30 g
ost). Then:

- every order takes it from stock, and taking a dish off the bill puts it back;
- the order form says how many can still be made ("3 left");
- when an ingredient runs out, every dish that uses it shows **No Ost left**
  and can't be ordered, by waiters or guests; **Add more Ost** on the Stock
  page brings them all back;
- "Not enough Spaghetti: only 2 more Carbonara can be made" if a waiter orders
  more than what's left. Nothing is half-added.

### Today's special (Something not on the menu)

**Something not on the menu** on the order form asks **Keep it on the menu?**

- **No, only this order:** as before (Öppen mat), just on this bill.
- **Yes, today only** or **Yes, until a day I pick:** it goes on the menu in
  **Dagens special** (the first section on the order form) so the next waiter
  just taps it, and it's off the menu by itself after its last day. It can
  also use ingredients from stock, like any dish. Guests don't see it on their
  phones (it has no allergens filled in); the manager can change that on the
  Menu page, where it shows **Until …** or **Ended**. Typing the same name
  again changes the special (price, last day) instead of adding a second one.

### Food or drink

On the **Menu** page each item is **Food** (goes to the chef, also for takeaway)
or **Drink** (only at the tables, never to the chef, never on the takeaway menu).

### Allergens

When guests order on their phone, nobody asks them about allergies, so the menu
has to say it. On the **Menu** page, **Edit** a dish and tick everything it
contains, from the 14 main allergens: gluten, crustaceans, egg, fish, peanuts,
soy, milk, nuts, celery, mustard, sesame, sulphites, lupin and molluscs.

- Guests see **Contains: fish, milk** under the dish on their phone at the table
  and on the takeaway page (in Swedish or English).
- Above the menu: "Allergic to something? The main allergens are listed under
  each dish. If you're not sure, ask your waiter before you order." On the
  takeaway page it says to call the restaurant (`RESTAURANT_PHONE`), or to write
  the allergy in the notes.
- Staff see the same line on the order page, and the Menu page says **No
  allergens ticked** for dishes nobody has checked yet.
- Ask the chef to go through every dish once. The list is only as good as what's
  ticked.

---

## 9. Guests order and pay with the table's QR code

Every table has a QR sticker. Guests scan it with their phone camera, and each
phone becomes one guest at that table: **Guest 1, Guest 2, Guest 3…** (they can
add their first name, so the waiter knows whose plate is whose). Everything they
order is theirs on the table's bill, so the bill is split by itself.

### Print the stickers

**Orders → Table QR codes** (`/admin/tables/qr`) shows one sticker per table,
in Swedish and English. Press **Print**, cut them out, and stick one on each table.

If a photo of a sticker gets around (someone could "order" from home), press
**New code** under that table and print it again: the old sticker stops working.

### The table opens by itself

Scanning only works while a table has guests, so nobody can order at an empty
table (or from home with a photo of the sticker). QR orders turn on by themselves
when:

- a booking is marked **Arrived**,
- walk-ins are **seated** at a table, or
- staff take the **first order** at a table.

The table's page in **Orders** shows "QR orders are on", with **Turn off** to stop
new phone orders at any time (and **Turn on** if needed). When the bill is paid,
QR orders turn off by themselves. A table that was opened but where nobody
ordered is cleared away overnight.

### Booked tables: the table code

Every table booking gets a **4-digit table code**, in the confirmation email and
on the booking page. From 30 minutes before the booking, scanning that table's
sticker says **"This table is booked for 19:00. Did you book it? Type your table
code."** Nobody else can order there.

- The right code checks the guests in (like staff pressing **Arrived**), opens
  the table and makes that phone Guest 1. Their friends then just scan and join.
- Guessing doesn't work: after 5 wrong codes a phone waits 10 minutes (and a
  table takes at most 10 wrong codes in 10 minutes from anywhere).
- If they don't come within **20 minutes** of their time, the booking is a
  no-show: the code stops working and the table is free for others.
- No code? Staff press **Arrived** for them as before.

New guests are never put on an earlier party's bill: if a table still has an
unpaid bill with orders on it, it isn't opened for the new guests until that
bill is paid.

### What the guest sees

1. **Start ordering** → "Guest 3 · Table 5".
2. The menu with **+ Add** buttons and a **Send order** bar. Prices always come
   from the menu: a guest can't change them.
3. **Your order**: what they ordered, with **Being made** / **Ready** for food,
   and **Your part**.
4. **Pay here:**
   - **Pay my part · 23,40 €**
   - **Pay for the whole table · 61,20 €** (asks once more before paying).
     Everyone else is then marked as paid by them, and their pages update by
     themselves.
5. **Table 5**: every guest at the table, their part, and **Paid / Not paid**.

They can also just pay the waiter at the end, as before: the card terminal and
cash work exactly like always (**Split the bill** shows phone guests with a 📱).

Scanning again on the same phone brings the guest back to their own page, not a
new guest. A table can have up to 12 guests.

### Call the waiter from the phone

Every guest's page has a **Call the waiter** button. It works like the button on
the table: the **Tables** screen (and the top of **Orders**) chimes and shows
"Table 5 · Wants a waiter · From the phone of Guest 2 · Anna". Staff tap **Done**
when they've been there, and the guest's page updates by itself.

During **3 meals** the page also has **Ready for meal 2** (and at the end
**We're done with all 3 meals**), exactly like pressing the table button. A
"Call the waiter" during the deal stays a call for a waiter, so it's never
mistaken for "bring the next meal".

### Where the orders go

Guests can also just ask the waiter. On the table's page, **Who is it for?**
lets staff put what they type in on one guest's part (**Guest 3**) instead of
**The table** (nobody's yet), so the split stays right without extra taps.

- **Food** goes to the **chef's screen** like any other order, with whose it is:
  **Laxfilé · Guest 1 · Anna · → no dill**.
- **Drinks** go to **Drinks to bring** at the top of **Orders**: "Table 5 ·
  2× Läsk 33 cl (Guest 1 · Anna)". Tap **Brought** when they're on the table.
  The chef never sees drinks.
- On the table's page each line shows whose it is.

### A waiter checks phone orders first (optional)

At the top of **Orders** there's a switch: **Phone orders are checked first**.
It's meant for the start, or for busy nights when someone might "prank order".

- **On:** a new phone order waits under **New phone orders** (with a chime) and
  a waiter taps **Accept** or **Reject**. Only accepted orders reach the chef.
- **Nobody taps within 5 minutes:** it's accepted by itself, so a guest never
  waits forever because the staff were busy.
- The guest sees "Your waiter checks your order…" with the minutes left. A
  rejected order says "Your waiter didn't put this order through. They'll come
  and talk to you." Rejects are written in the **Activity** list.
- While an order is waiting, that guest can't pay yet, and nobody can pay for
  the whole table, because the bill isn't final. Accepting or rejecting it (or
  the 5 minutes) unlocks paying again.
- **Off** (the default): orders go straight to the kitchen. Turning it off
  accepts everything that's still waiting.

The 5 minutes is one setting: `AutoAcceptAfter` in `db/phoneorders.go`.

### Two people paying at the same time: the payment lock

When someone presses **Pay for the whole table**, the table is locked for them
at once. The server does this in one step, so even if two phones press it in the
same second, only one gets the lock.

- Everyone else immediately sees **"Guest 2 is paying for the whole table…"**
  with a countdown, and their pay buttons and menu are hidden.
- **The payer cancels** (or presses back on the payment page) → the lock lifts at
  once.
- **The payer walks away** → the lock lifts after **3 minutes** (one setting:
  `PayHoldFor` in `db/guests.go`). Their unfinished payment page is cancelled at
  Stripe first, so it can never go through late.
- **It works the other way too:** while someone pays their own part, nobody can
  start "pay for the whole table" (others can still pay their own parts).
- While a part is being paid on a phone, staff can't take a card or cash payment
  for it either ("A guest is paying for that on their phone right now").

### The receipt: phone, email or paper

After paying on the phone (or being paid for), each guest picks how they want
their receipt:

- **Show it on my phone**: their own online receipt.
- **Email it to me**: they type their email and it's sent at once. The address
  is used for that one email and not saved (at most 3 emails per guest).
- **Bring me a printed receipt**: it appears on the Orders screen under
  **Receipts to print**. Tap **Print** (just that guest's receipt), bring it, tap
  **Given**.

Guests who pay at the card terminal get the terminal's receipt, as before.

### Who hasn't paid yet (in red)

As soon as some guests at a split table have paid and others haven't, the Orders
screen shows a red **Not paid yet** box, for example
**Table 5 · Guest 2 hasn't paid · 10,00 € · 30,00 € of 40,00 € paid**, and the
table's tile gets a red border. It stays until everything is settled.

- A guest paying on their phone right now shows as "Guest 2 is paying now".
- Something the waiter added for "the table" that nobody has paid for shows as
  "3,00 € isn't anyone's yet". Any guest can then pay it with **Pay for the
  whole table** on their phone, or staff give it to a guest in **Split the bill**.
- On the split page, the guests who still have to pay are red too.

### Payments

**Paid on phones.** When a guest pays on their phone, the Orders and Tables
screens show it at the top, with a chime: "Table 5 · Erik paid 19,90 € online
(+ 1,99 € tip) · for the whole table · Table is free". It stays (and reminds)
until a waiter taps **OK**, so nobody chases a guest who already paid. It's
also in **Paid today** (Online at the table) and the table's history.

Phone payments use **Stripe Checkout**, the same as deposits: Apple Pay and
Google Pay appear by themselves on phones that have them, and anyone can pay by
card. No extra setup is needed beyond section 4. In demo mode a stand-in page
lets you try it without paying anything.

- **Paid today** in Orders shows a separate **Online at the table** line.
- Receipts and the table's history say **Paid online**.
- An online payment can't be undone with a tap: to give money back, refund it
  in Stripe.
- Stripe can't take less than 0,50 €, so a smaller part is paid to the waiter.

### Tips

After pressing **Pay my part** or **Pay for the whole table**, the guest
chooses: **No tip**, **5 %** or **10 %**, each showing the amount and the new
total. Nothing is chosen for them.

- The tip is its own line on the payment page ("Tip for the staff (10 %)").
- The guest's page and receipt show it: "Tip for the staff 2,69 €".
- **Paid today** in Orders shows **Tips on phones (for the staff)**, and so does
  the end-of-day email. Tips are kept apart from the food and drink total, so the
  day's sales stay right.
- Tips at the card terminal work as before, on the terminal.

The choices are one setting: `TipChoices` in `db/guests.go`.

### Try it with phones (demo)

1. Start E-Meal Kitchen on your Mac and open it on your phone on the same Wi-Fi
   (section 7, "Open it on your phone").
2. Open **Orders → Table QR codes** *on that address* (not `localhost`), so the
   codes point at your Mac.
3. Turn on QR orders for a table, scan its code with two or three phones, order,
   and press **Pay for the whole table** on one of them. Watch the others update.

### Dish photos

On the **Menu** page, press **Edit** on a dish and choose a photo (straight from
a phone camera is fine, up to 15 MB). Guests then see it next to the dish on
their phone at the table and on the takeaway page.

- The photo is turned the right way up, made smaller (1200 pixels at most) and
  cleaned: the hidden information phones put in photos, like where it was taken,
  is removed.
- **Remove photo** takes it off again.
- Photos are kept in the `photos` folder next to the data file
  (`data/photos`). Back it up together with `data/emeal.json`.

---

## 10. Everything the old till did (Restolution)

E-Meal Kitchen replaces the old till. This is where each of its buttons went.

| Old till | In E-Meal Kitchen |
|---|---|
| Servitör + number, Klockkort | **Till keypad** (`/till`): number + PIN to log in, **Clock in** / **Clock out** |
| Utrymme 1, Utrymme 2, terassen, table map | **Room tabs** on Orders and Tables, the same table numbers |
| Lunch, Mat, After Work, Spirit/Cocktails, Vin, Alkoholfritt | **Menu sections** and **Search the menu** on the order page |
| Öppen mat, Öppen alkohol, Öppen A-fri | **Something not on the menu**: type a name, price and type (food, drink, drink with alcohol) |
| Extra biff, Byt till sötpotatis, dips | **Tillbehör & dippar** on the menu |
| VÄNTA VARMT, VARMA FRAM, FRAM EFTERRÄTT | **Vänta varmt** on the order, **Mains now** / **Dessert now** on the bill |
| Fritt köks meddela | **Message to the kitchen** |
| Rabatt | **Discount** per line (Change) or on the whole bill |
| Dela nota, Stol, Byt #stol | **Split the bill** (guests 1, 2, 3…) |
| För till bord | **Move this bill to** |
| Mellannota (Proforma) | **Show the bill**: marked "Pro forma · not a receipt" |
| Kontant, Kort betalning | **Paid in cash**, **Paid at the terminal** |
| Betalmedel | **Other ways to pay**: ePassi, Smartum, Edenred, gift card, lunch coupons, part of the bill |
| Kvar att betala | Paying in parts: the bill shows **Left to pay** |
| Försäljning av presentkort / lunchkuponghäfte | **Gift cards and lunch booklets** (`/admin/giftcards`) |
| Föregående kvitto | **Table history**: every bill and receipt |
| Cocktail Hour buttons | **Cocktail-hour price** on each drink |
| 3-Rätter paket, AW menus | **After Work** set menu |
| Receipt with VAT 25,50 / 13,50 and FO-nummer | Every receipt and the end-of-day email |

Not built: **Rum** (charging a hotel room), customer accounts (**Kund funktioner**),
and the till's own printer and cash drawer: E-Meal Kitchen prints from the browser (a receipt
printer set as the default printer works; many open the cash drawer when they print).

### Rooms and tables

**Orders** and **Tables** have tabs: **All rooms**, **Utrymme 1**, **Utrymme 2**,
**Terrassen**. The number on a tab is how many tables there have a bill (or, on
Tables, are waiting for someone). Each screen remembers its tab. A free table is a
small tile, so a whole room fits on one screen.

### The menu

The menu is copied from the printed menus and the old till: starters, the weekly
schnitzel, pasta, classics, burger, salads and bowls, kids' menu, desserts, sides
and dips, lunch, After Work, cocktails, coffee drinks, bubbles, wines (16 cl, 24 cl
and bottle), beer, cider, alcohol-free, soft drinks, coffee and tea.

- **Price missing:** prices that weren't on the photos (lunch dishes, sides and
  dips, 24 cl wine, coffee and tea, Briska fläder 0,5 %) are 0,00 € and marked
  **Price missing**. Guests never see them; staff type the price when they take
  the order. Set the prices on the Menu page.
- **Veckans schnitzel:** all four schnitzels are on the menu, only this week's is
  switched on (Jägarschnitzel to start with). Switch them weekly.
- **VAT:** every dish and drink is food (13,5 %) or a drink with alcohol
  (25,5 %); drinks up to 1,2 % count as alcohol-free. The Menu page's **Type** sets
  it. The rates are `VATFood` and `VATAlcohol` in `schedule/schedule.go`.
- **Course:** starter, main course or dessert, for "Vänta varmt" (below).
- The demo menu of earlier versions is replaced by this one the first time the new
  version starts. A menu the manager has built is never touched.

### Courses: Vänta varmt, Varma fram, Fram efterrätt

When the order has starters, **Vänta varmt** (ticked by default) keeps the mains
away from the chef until you say so; desserts ordered with the meal wait too.

- The chef sees them greyed out under **Waiting for the go**, so he can get
  ready, and can **Start now** himself.
- On the table's bill, **Mains now (Varma fram)** and **Dessert now (Fram
  efterrätt)** send them to the chef with a chime.
- The tile on Orders says **Waits for your go**; a guest who ordered on their
  phone sees "Comes after the starter".
- Mains without starters go straight to the chef, as before.

### Message to the kitchen

At the bottom of the bill: **Message to the kitchen**, e.g. "Guest 2 is allergic to
nuts". It pops up on the kitchen screen with a chime; the chef taps **Seen**.

### Discounts

- One line: **Change** on the bill line, then **Discount**: 10, 15, 20, 25, 30,
  50 % or 100 % (on the house).
- The whole bill: **Discount, move or void** → **Discount on the whole bill**.
  Dishes in a set menu (price 0) are left alone.
- The bill shows the discount on each line and **Discounts given**; receipts show
  it on each line. Every discount is in the **Activity** list with who gave it.
- To change a discount after part of the bill is paid, take those payments back first.

### After Work and other set menus

**After Work** is a section on the menu that only staff see (guests can't order it
on their phone):

- **After Work · 3 rätter** 30,00 € and **After Work · 1 rätt** 15,00 € have the
  price and never go to the chef.
- **After Work · dryck** 5,00 € (with alcohol) or **alkoholfri dryck** 5,00 €:
  so "3 rätter + dryck" is 35 € and "1 rätt + 1 dryck" 20 €.
- The dishes (AW Hummersoppa, AW Jägarschnitzel…) are in the price: add the ones
  the guest picks, and they go to the chef like any dish, with Vänta varmt.

The 15 € + 5 € split of "1 rätt + 1 dryck" follows the old till's "AW Dryck 5"
button: check it with the owner. On the Menu page, **Set menu** makes any dish a
set menu's price line or a dish in a set menu, for other set menus.

### Cocktail hour

Each drink can have a **Cocktail-hour price** (Menu → Edit). During cocktail hour
that price is charged, on the staff order page and on guests' phones, with a
**Cocktail hour** label. The times are `HappyHours` in `schedule/schedule.go`
(16:00–18:00 every day to start with). No drink has a cocktail-hour price yet:
the old till had "Hour" buttons, but not their prices.

### Ways to pay

On the bill, besides **Paid at the terminal** and **Paid in cash**:

- **Other ways to pay → ePassi / Smartum / Edenred** pays the whole bill with a
  lunch benefit card. On a split bill each guest has a **Lunch card** choice.
- **Pay part of the bill** ("Kvar att betala"): choose how and how much, e.g. 30 €
  on Edenred, then the rest at the terminal. The bill shows each payment and
  **Left to pay** until it's all paid, then it closes by itself.
- **Take back the last payment** undoes a mistake (a gift card gets its money back).
- While part of a bill is paid, its lines can't be changed or split (new orders
  can still be added), and guests can't pay it on their phones: pay the rest, or
  take the payments back first.

**Paid today** on Orders and the end-of-day email list every way of paying.

### Gift cards and lunch booklets

**Orders → Paid today → Gift cards and lunch booklets** (`/admin/giftcards`):

- **Sell a gift card:** the value, paid at the terminal or in cash. It prints a
  card with a code like `PK-7K4M-Q2PX`.
- **Sell a lunch booklet:** how many coupons, what one coupon pays (one lunch, up
  to e.g. 12,90 €) and the booklet's price. Code `LH-…`.
- **Paying:** on the bill, **Other ways to pay → How: Gift card** (or **Lunch
  coupons** and how many), type the code. A gift card pays up to what's on it and
  what's left on the bill; the rest is paid another way. Lunch coupons: E-Meal Kitchen
  refuses more coupons than needed (if one less already covers what's left), so
  none are wasted.
- **Look up a card** shows what's left and when it was sold. The manager can void
  a card sold by mistake (if it hasn't been used).
- Codes have no 0/O, 1/I or 5/S, so they're easy to read out and type.
- In the reports, gift cards and lunch coupons used are listed apart ("paid when
  sold"), and cards sold are listed on their own. Ask the accountant how they want
  them booked.
- **On a split bill**, each guest card has **Gift card, lunch coupons or part of
  it**: type the code, **Add payment**. The guest card then shows what the card
  paid and **Left to pay**; **Terminal**, **Cash** or a lunch card pay the rest.
  A 50 € card for a 12 € part pays the 12 € and keeps 38 €. **Take back the last
  payment** gives the card its money back. A guest who paid some of their part
  pays the rest on their own (not "together" with others), and with the waiter,
  not on their phone: their phone says how much is left. Their receipt lists
  both payments.

### Receipts with VAT

Every receipt and **Show the bill** look like the old till's:

- the restaurant's name, address and phone at the top (`RESTAURANT_ADDRESS`,
  `RESTAURANT_PHONE`)
- each line, with its discount
- **VAT %, Without VAT, VAT, With VAT** for 25,50 and 13,50, worked out from the
  prices the same way (84,00 € of wine = 66,93 + 17,07 VAT)
- what was paid how, and **Left to pay** on a bill paid in parts
- "Thank you for your visit, welcome back!" and the **Business ID** (FO-nummer,
  `BUSINESS_ID`)
- **Show the bill** before paying says **Pro forma · not a receipt** at the top.

The end-of-day email adds up the VAT of the day's bills too.

### The shared till: PIN keypad and clock card

Like the old "Servitör" keypad:

1. The manager gives each waiter a **PIN** (4–6 digits) on **Staff**. Each waiter
   has a number (1, 2, 3…), shown before their name.
2. On the shared screen, the manager taps **Make this device a till** on **Staff**
   once. The keypad is at `/till` (add it to the home screen).
3. A waiter types their **number** and **PIN** and taps **Log in**. Their name is
   at the top, and everything they do with money is under their name. **Lock** at
   the top logs them out for the next person. A PIN login lasts at most 12 hours.
4. **Clock in** / **Clock out** on the same keypad is the clock card. The manager
   sees the hours on **Staff → Hours (clock card)**, per week and per person, and
   can fix a forgotten clock-out.

A PIN is short, so the keypad only works on devices made a till: nobody can try
PINs from the internet. After 5 wrong PINs for one number (or 10 on one till), the
keypad stops taking PINs for a while (up to 15 minutes). Waiters can still log in with their username and password
anywhere.

---

## 11. Feedback after the visit

Guests can say how it was, privately, and are then invited to review the
restaurant on Google.

**Where guests find it:**
- a **review sticker** (★★★★★ "How was it? Tell us"), printed with the table
  stickers on **Orders → Table QR codes**. Put it on the tables or by the door.
- the **"How was it? Tell us"** link on every receipt.

**What the guest does** (no login, no account):
1. Taps 1 to 5 stars.
2. Writes a comment if they want (under 4 stars it asks "What could we have
   done better?"), and an email if they'd like a reply.
3. Sees "Thank you!" and a **Review us on Google** button.

**What the restaurant gets:**
- Every rating is emailed to `MANAGER_EMAIL`, good or bad, and kept on the head
  manager's **Feedback** page (`/owner/feedback`): the 30-day average, how many
  were under 4 stars, and every comment.
- Nothing is published. The feedback is private.

**Why every guest gets the Google link (not only happy ones):** Google doesn't
allow asking only happy guests for reviews ("review gating"), and EU consumer
rules forbid hiding bad reviews. Profiles caught doing it can lose their reviews
or be shown a warning. Asking everyone is allowed, and the private feedback
first often catches a problem before it ends up on Google.

**Setup:** in the Google Business Profile, open **Ask for reviews** and copy the
link (`https://g.page/r/…/review`). Put it in `.env` as `GOOGLE_REVIEW_URL`.
Without it, guests still give feedback, just without the Google button.

---

## 12. Waiters' own logins and the activity list

The manager adds each waiter on **Staff** (`/admin/staff`): their name, a
username and a password (at least 8 characters).

**What a waiter can open:** Tables, Orders, Kitchen, Bookings and Calendar. Their
name shows at the top. **Not** the Menu, Closed days, Staff or Activity, and not
the head manager's pages.

**Activity** (`/admin/activity`) shows, per day, who did what with money:
- took a card or cash payment, or took one back
- voided a bill, took something off a bill, changed a price
- moved a bill to another table, rejected a phone order
- refunded or cancelled a booking

For example: **18:05 Lisa · paid in cash · Table 5 · Guest 1 · 26,90 €**. Choose
one person from the list to see only theirs.

- **New password:** gives the waiter a new password and logs them out everywhere.
- **Remove:** they can't log in any more, on any device, at once. Their name
  stays in the activity list.
- The manager and head manager logins in `.env` work as before.
- Passwords are never stored, only a slow, salted fingerprint of them, so even
  someone holding the data file can't read them.

---

## 13. End-of-day email

30 minutes after closing, the owner gets the day in one email:

- what was paid at the tables: card terminal, cash and online
- online deposits: paid, refunded, kept
- table bookings (came / no-shows), takeaway orders, 3 meals, best sellers
- the day's feedback
- **Needs a look:** bills still open, voided bills and similar. Or "Nothing left
  open. Good night!"

It goes to `REPORT_EMAIL` (or `MANAGER_EMAIL` if that's empty). Days when
nothing happened don't get an email. If the server was off at that time, it's
sent the next morning before opening.

The head manager can see the same summary for any day on **Today → End-of-day
email** (`/owner/summary`).

---

## 14. Manager dashboard (`/admin`)

- **Tables:** the live screen for the table buttons and 3 meals (section 7).
- **Orders** and **Kitchen:** bills at the tables and the chef's screen (section 8),
  drinks guests ordered on their phones, QR orders on or off, and the waiter check
  of phone orders (section 9). **Sold out?** at the bottom of the kitchen screen.
- **Today:**
  - all bookings and takeaway orders for a day, and the six tables right now
  - **Arrived** to check guests in (no-show after 20 minutes otherwise)
  - **Phone booking:** for guests who call. No deposit, confirmed at once, a table is picked automatically (or choose one). Add their email and they get a confirmation with the change/cancel link and a reminder.
  - **Walk-in:** one tap seats guests who came in without booking, on a table that's free for the next 2 hours. Shown while the restaurant is open.
  - **Picked up** on takeaway orders, once the guest has collected and paid the rest
  - tags on each booking: *Phone*, *Walk-in*, the occasion (*Birthday*, *Anniversary*, *Business*), *Visited 3× before*, and a warning for guests who didn't turn up before
  - the **waiting list** for the day
  - cancel buttons. The highlighted button follows the policy, and refunds go through Stripe automatically.
- **Calendar:** a month view with bookings per day, and the phone-calendar link.
- **Menu:** add, edit and price dishes and drinks, add photos, and switch dishes off (until you switch them on again).
- **Closed days:** close for holidays or private events. Guests can't book those days.
- **Staff** and **Activity:** waiters' logins, PINs and who did what with money (section 12); **Hours** from the clock card, and the shared till (section 10).
- **Gift cards and lunch booklets:** from **Orders → Paid today** (section 10).

Waiters see Tables, Orders, Kitchen, Bookings and Calendar only.

---

## 15. Money pages (`/owner`)

The manager opens these from the top bar (Money today, Charts, Table buttons,
Feedback). Waiters can't. `OWNER_USER` / `OWNER_PASSWORD` in `.env` is an
optional second manager login with the same rights; leave the password empty
to switch it off.

**Today** (the page you land on):
- **Made today**, compared with the same weekday last week
- **The month so far**, compared with the same days last month

- **Deposits:** paid, refunded, cancelled but kept, left without paying
- **Takeaway:** orders, money still to collect at pickup, cancelled, average order
- **Everything booked for the day**, with its status

Use the arrows or the date box to look at any other day.

**End-of-day email:** the button at the top of **Today** shows the day's summary email (section 13).

**Feedback:** guests' ratings and comments, and the 30-day average (section 11).

**Table buttons:** each table's secret button code, last signal and battery (see section 7).

**Charts:**
- **Day by day** (this month against last month) or **month by month** (the last 12 months)
- Switch between **money, table bookings, guests, takeaway orders and no-shows/cancellations**
- Hover or tap a bar for the exact numbers. Every chart can also be shown as a table.
- **Busiest days and hours, best-selling dishes, and what happened to bookings**
- **Download as spreadsheet:** every booking of the month, opens in Excel

Only money paid through the website is counted. What guests pay at the table goes through the restaurant's till.

## 16. How it feels to use

- **Booking:** picking guests, day and time happens instantly with no page reloads. A summary with the pay button follows you (a bar at the bottom on phones, a card on the side on computers).
- **Takeaway:** each dish has an **Add** button, and your order builds up in a cart.
- **Manager:**
  - live table tiles
  - one main button per booking (**Arrived**), with everything else under **More**
  - a confirmation window for anything that can't be undone
  - a short message after every action
- Pages fade into each other instead of flashing, and links start loading as soon as you hover them.
- Styles and scripts are cached, and they refresh automatically whenever they change.

## 17. Get more bookings (no code needed)

- **Google:** in the restaurant's Google Business Profile, add `https://<your-domain>/book` as the reservations link and `/takeaway` as the order link (Edit profile, look for the booking or reservation links; Google moves the menu around now and then). Guests then book straight from Google Search and Maps.
- **Instagram and Facebook:** put the `/book` link in the bio and on the page's "Book now" button.
- **MobilePay, Apple Pay and Google Pay:** turn them on in Stripe (**Settings → Payment methods**). The payment page shows them automatically, nothing to change here. MobilePay is what most guests on Åland use.
- **Add to home screen:** guests can add the site to their phone like an app. Staff should do the same from the dashboard: on the iPad or phone, open `/admin/orders`, then Share → **Add to Home Screen**. It opens straight on **Orders**, with its own dark "E-Meal Kitchen Staff" icon.

## 18. Phase 2 (not built yet)

- **SMS to the manager and SMS reminders to guests:** the hook is already in place, `notifyManagerSMS` in `handlers/public.go`. It needs a provider like Twilio.
- **WhatsApp payment link:** needs Meta WhatsApp Business verification and approved message templates. Start that paperwork early.
- **Food pre-order** with table bookings.
- **Floor plan:** the tables drawn where they stand, like the old till's map
  (today they're tiles, room by room).
- Have the owner's accountant check the receipts and the end-of-day email
  (VAT, gift cards, lunch booklets) once before going live.
