# The table button

A small box on each table with one big button. Guests press it when they've
finished a meal (or want a waiter). The table lights up on the staff **Tables**
screen within a few seconds.

Inside: a tiny Wi-Fi board that sleeps until the button is pressed, wakes up,
joins the restaurant's Wi-Fi, sends its table's secret code to the booking
system, blinks its LED to show it worked, and goes back to sleep. Once a day it
wakes by itself to report its battery.

> **Not tested on real hardware yet.** The code is written for the board below
> and checked as far as possible without one. Build **one** button first, test
> it against your Mac (step 5), then make the rest.

---

## 1. Parts for one table

| Part | Notes |
|---|---|
| **Seeed Studio XIAO ESP32C3** | The brain: Wi-Fi, USB-C, and it charges a battery by itself. Around £5 at The Pi Hut. |
| **Push button** (momentary, "normally open") | Big and easy to hit, e.g. a 30–60 mm arcade-style button. |
| **LED** (any colour) + **220 Ω resistor** | Shows the guest the press got through. |
| **100 kΩ resistor** | Keeps the button line steady while the board sleeps. |
| **3.7 V LiPo battery**, e.g. 500–1000 mAh, with wires | Soldered to the BAT+ / BAT− pads under the board. Charged through the board's USB-C port. |
| *Optional:* **2 × 220 kΩ resistors** | Let the button measure and report its battery. Without them, the battery shows "–". |
| **A small box** | Easy to wipe clean, with holes for the button, the LED and the USB-C port. |

You also need a soldering iron and a USB-C cable.

## 2. Wiring

```
                 XIAO ESP32C3 (USB-C at the top)
                ┌──────────────────────┐
                │ D0               5V  │
   button ──────┤ D1  (GPIO3)     GND  ├──── GND rail
   battery mid ─┤ D2  (GPIO4)     3V3  ├──── 3V3
   LED + 220Ω ──┤ D3  (GPIO5)     D10  │
                │ ...                  │
                └──────────────────────┘
                   BAT+  BAT−  (pads underneath) ── LiPo battery

  Button:      D1 ──[button]── GND
  Pull-up:     D1 ──[100 kΩ]── 3V3
  LED:         D3 ──[220 Ω]──(LED long leg ▶ short leg)── GND
  Battery level (optional):
               BAT+ ──[220 kΩ]──┬──[220 kΩ]── GND
                                └── D2
```

Check the battery's polarity before soldering: red to **BAT+**.

## 3. Install the tools (once)

1. Install **Arduino IDE 2** (free) from arduino.cc.
2. **Settings → Additional boards manager URLs**, add:
   `https://espressif.github.io/arduino-esp32/package_esp32_index.json`
3. **Tools → Board → Boards Manager**, search **esp32**, install **esp32 by Espressif Systems** (version 3.x).

## 4. Program a button

1. Open `button/ozmo-button/ozmo-button.ino` in Arduino IDE.
2. Next to it, copy `secrets.example.h` to **`secrets.h`** and fill it in:
   - `WIFI_NAME`, `WIFI_PASSWORD`: the restaurant's Wi-Fi. It must be **2.4 GHz**, and a network without a login page (a guest Wi-Fi where you tick "I accept" won't work: make a separate network for the buttons on the router if needed).
   - `SITE_ADDRESS`: where the booking system runs (see step 5 for testing, step 6 for live).
   - `BUTTON_CODE`: log in as head manager, open **Table buttons**, tap the dots next to the table and copy its code. **One code per table.**
3. **Tools → Board → esp32 → XIAO_ESP32C3**. Set **USB CDC On Boot: Enabled** (so you can see its messages).
4. Plug the board in, pick its **Port**, press **Upload**.
5. Open **Tools → Serial Monitor** at 115200 to see what it does.

On its first start the button checks in. The LED then:

| LED | Means |
|---|---|
| one long glow (1.5 s) | sent: staff can see it |
| two glows | sent, and the table was already waiting (pressed again) |
| fast flicker | **not** sent: Wi-Fi, address or code is wrong, or the site is down |
| slow blink right after a press | joining the Wi-Fi |

The **Table buttons** page then shows a "last signal" for that table.

**Uploading again later:** a sleeping board doesn't show up as a port. Hold the
**BOOT** button on the board, tap **RESET**, let go of BOOT, then upload.

## 5. Test with your Mac

1. On the Mac: `cd ozmo-booking && DEMO_CLOCK=2026-10-31T18:05 go run .` (a pretend Saturday, so 3 meals can run).
2. Find the Mac's address: `echo "http://$(ipconfig getifaddr en0):8080"`.
3. In `secrets.h`, set `SITE_ADDRESS` to that address, with the Mac and the button on the same Wi-Fi. Upload.
4. Open `http://localhost:8080/admin/tables`, start 3 meals on the button's table, then press the button: the tile turns orange, **"Ready for meal 2"**.

If the Mac asks whether to accept incoming connections, click **Allow**.

## 6. Going live

1. Set `SITE_ADDRESS` to the real site, e.g. `"https://book.ozmo.ax"`.
2. Fill in `ROOT_CA`, so the button only ever talks to the real site. For a site
   using Let's Encrypt certificates, copy **ISRG Root X1** from
   https://letsencrypt.org/certificates/ (the whole text, from `-----BEGIN` to
   `END CERTIFICATE-----`) between the two lines in `secrets.h`. For another
   certificate company, open the site in Chrome, click the icon left of the
   address, and export the **top** certificate of the chain.
3. Upload to every button, each with its own `BUTTON_CODE`.

## 7. Day to day

- **Charging:** plug a USB-C cable into the box. The Tables screen says when a button is below 20 %. How long a charge lasts depends on the battery and how often it's pressed; the board itself uses very little while asleep (Seeed lists about 44 µA).
- **A button is lost or stolen:** head manager → **Table buttons → New code** for that table. The old code stops working at once. Program the new code into the replacement.
- **A button wakes up by itself:** check the 100 kΩ resistor between D1 and 3V3.
- **"Hasn't been heard from in 2 days":** it's flat, out of Wi-Fi range, or the Wi-Fi password changed.

## 8. Security

- `secrets.h` holds the Wi-Fi password and the code: it's never shared or put on GitHub (`.gitignore` skips it).
- Each code is a random 32-character code. The site never accepts it in the address, and stops listening to an address after 20 wrong codes in 10 minutes.
- With `ROOT_CA` filled in, a fake Wi-Fi network can't pretend to be the site and collect the code.
