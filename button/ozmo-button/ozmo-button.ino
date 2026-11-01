// Ozmo table button
//
// One of these sits on every table. When guests press it, it wakes up,
// joins the restaurant's Wi-Fi, tells the booking system "table N pressed",
// shows the answer on its LED and goes back to deep sleep. Once a day it
// also wakes by itself to report its battery, so the head manager can see
// that every button is alive (Table buttons page).
//
// Board: Seeed Studio XIAO ESP32C3 (Arduino core "esp32" by Espressif, 3.x).
//
// Wiring (see button/README.md for a drawing):
//   push button : D1 (GPIO3) and GND            pressing pulls D1 low
//   pull-up     : D1 -> 100 kΩ -> 3V3           keeps D1 high while asleep (Espressif's advice)
//   LED         : D3 (GPIO5) -> 220 Ω -> LED -> GND
//   battery     : LiPo on the BAT+ / BAT- pads underneath (charges over USB-C)
//   battery level (optional): BAT+ -> 220 kΩ -> D2 (GPIO4) -> 220 kΩ -> GND
//
// Before uploading: copy secrets.example.h to secrets.h and fill it in.

#include <WiFi.h>
#include <HTTPClient.h>
#include <WiFiClientSecure.h>
#include "esp_sleep.h"
#include "soc/soc_caps.h"
#include "secrets.h"

// ---- pins (XIAO ESP32C3) -------------------------------------------------
const int BUTTON_PIN = 3;   // D1. Must be GPIO 0–5: only those can wake the C3.
const int LED_PIN = 5;      // D3
const int BATTERY_PIN = 4;  // D2 (A2). Set to -1 if you skip the two resistors.

// ---- timing ----------------------------------------------------------------
const unsigned long WIFI_TIMEOUT_MS = 10000;  // give up joining Wi-Fi after 10 s
const unsigned long HTTP_TIMEOUT_MS = 8000;   // and waiting for the site after 8 s
const uint64_t CHECKIN_EVERY_HOURS = 24;      // "I'm alive" once a day

// ---- LED signals -------------------------------------------------------------
void led(bool on) { digitalWrite(LED_PIN, on ? HIGH : LOW); }

void blink(int times, int onMs, int offMs) {
  for (int i = 0; i < times; i++) {
    led(true);
    delay(onMs);
    led(false);
    delay(offMs);
  }
}

// Sent: one long glow. Already waiting (pressed again): two glows.
// Not sent: fast flicker, so the guest knows to wave at a waiter.
void showSent() { blink(1, 1500, 0); }
void showAgain() { blink(2, 500, 250); }
void showFailed() { blink(12, 80, 80); }

// ---- battery -----------------------------------------------------------------

// batteryPercent reads the LiPo through the two equal resistors (half the
// voltage reaches the pin). 3.3 V counts as empty, 4.2 V as full.
// 0 means "unknown" to the booking system.
int batteryPercent() {
  if (BATTERY_PIN < 0) return 0;
  pinMode(BATTERY_PIN, INPUT);
  uint32_t sum = 0;
  for (int i = 0; i < 8; i++) sum += analogReadMilliVolts(BATTERY_PIN);
  int mv = (int)(sum / 8) * 2;
  if (mv < 2500) return 0;  // nothing connected
  int pct = (mv - 3300) * 100 / (4200 - 3300);
  if (pct < 1) pct = 1;
  if (pct > 100) pct = 100;
  return pct;
}

// ---- Wi-Fi and the booking system -------------------------------------------------

bool joinWifi(bool showProgress) {
  WiFi.mode(WIFI_STA);
  WiFi.begin(WIFI_NAME, WIFI_PASSWORD);
  unsigned long start = millis();
  bool on = false;
  while (WiFi.status() != WL_CONNECTED) {
    if (millis() - start > WIFI_TIMEOUT_MS) return false;
    if (showProgress) {  // slow blink while connecting
      on = !on;
      led(on);
    }
    delay(150);
  }
  led(false);
  return true;
}

// post sends the button's secret code to path ("/api/press" or
// "/api/checkin"). It returns the HTTP status (200 = OK, 401 = wrong code,
// below 0 = no connection) and fills reply with the answer.
int post(const char *path, int battery, String &reply) {
  String url = String(SITE_ADDRESS) + path;
  bool https = url.startsWith("https://");
  if (https && strstr(ROOT_CA, "BEGIN CERTIFICATE") == nullptr) {
    Serial.println("https needs ROOT_CA in secrets.h");
    return -100;
  }
  WiFiClientSecure secure;  // declared before http, so they outlive it
  WiFiClient plain;
  HTTPClient http;
  bool started;
  if (https) {
    secure.setCACert(ROOT_CA);  // only talk to the real site
    started = http.begin(secure, url);
  } else {
    started = http.begin(plain, url);  // fine on the restaurant's own network, for testing
  }
  if (!started) return -101;
  http.setTimeout(HTTP_TIMEOUT_MS);
  http.addHeader("Authorization", String("Bearer ") + BUTTON_CODE);
  http.addHeader("Content-Type", "application/json");
  int status = http.POST(String("{\"battery\":") + battery + "}");
  if (status > 0) reply = http.getString();
  http.end();
  return status;
}

// ---- sleep -----------------------------------------------------------------------

// waitForRelease: a button still held down would wake the board again at
// once. Returns false if it's stuck (then only the daily timer wakes it).
bool waitForRelease() {
  unsigned long start = millis();
  while (digitalRead(BUTTON_PIN) == LOW) {
    if (millis() - start > 10000) return false;
    delay(20);
  }
  delay(50);  // let the contacts settle
  return true;
}

void sleepNow(bool wakeOnButton) {
  led(false);
  WiFi.disconnect(true);
  WiFi.mode(WIFI_OFF);
  if (wakeOnButton) {
#if SOC_GPIO_SUPPORT_DEEPSLEEP_WAKEUP
    // ESP32-C3: wake when the pin goes low (the 100 kΩ pull-up keeps it
    // high until someone presses).
    esp_deep_sleep_enable_gpio_wakeup(1ULL << BUTTON_PIN, ESP_GPIO_WAKEUP_GPIO_LOW);
#else
    // Classic ESP32 boards: use an RTC pin (e.g. GPIO 33) as BUTTON_PIN.
    esp_sleep_enable_ext0_wakeup((gpio_num_t)BUTTON_PIN, 0);
#endif
  }
  esp_sleep_enable_timer_wakeup(CHECKIN_EVERY_HOURS * 3600ULL * 1000000ULL);
  esp_deep_sleep_start();
}

// ---- what happens each time it wakes ---------------------------------------------------

void setup() {
  pinMode(LED_PIN, OUTPUT);
  led(false);
  pinMode(BUTTON_PIN, INPUT_PULLUP);
  Serial.begin(115200);

  esp_sleep_wakeup_cause_t why = esp_sleep_get_wakeup_cause();
  bool pressed = why == ESP_SLEEP_WAKEUP_GPIO || why == ESP_SLEEP_WAKEUP_EXT0;
  bool firstStart = why == ESP_SLEEP_WAKEUP_UNDEFINED;  // battery just connected, or reset
  int battery = batteryPercent();

  if (pressed) {
    led(true);  // instant "got it" while it connects
    String reply;
    int status = joinWifi(true) ? post("/api/press", battery, reply) : -1;
    Serial.printf("press: %d %s\n", status, reply.c_str());
    if (status == 200 && reply.indexOf("\"again\":true") >= 0) {
      showAgain();
    } else if (status == 200) {
      showSent();
    } else {
      showFailed();
    }
  } else {
    // Daily check-in (silent), or the very first start: then the LED shows
    // whether the Wi-Fi and the code work, so you know the setup is right.
    String reply;
    int status = joinWifi(firstStart) ? post("/api/checkin", battery, reply) : -1;
    Serial.printf("check-in: %d %s (battery %d%%)\n", status, reply.c_str(), battery);
    if (firstStart) {
      if (status == 200) showSent();
      else showFailed();
    }
  }

  sleepNow(waitForRelease());
}

void loop() {
  // Never reached: the board sleeps at the end of setup() and starts over
  // from setup() when it wakes.
}
