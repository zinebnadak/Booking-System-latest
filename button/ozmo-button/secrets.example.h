// Copy this file to secrets.h (same folder) and fill it in, once per button.
// secrets.h is never shared or put on GitHub: it holds the Wi-Fi password
// and the button's secret code.

#pragma once

// The restaurant's Wi-Fi (2.4 GHz: the board can't use 5 GHz networks).
const char *WIFI_NAME = "Restaurant-WiFi";
const char *WIFI_PASSWORD = "wifi-password";

// Where the booking system runs, without a slash at the end.
//   Testing on your Mac:  "http://192.168.1.23:8080"  (your Mac's address on the same Wi-Fi)
//   Live:                 "https://book.ozmo.ax"
const char *SITE_ADDRESS = "http://192.168.1.23:8080";

// This table's secret code: the head manager's "Table buttons" page,
// "Secret code" (tap the dots to see it all). One code per button.
const char *BUTTON_CODE = "paste-the-32-character-code-here";

// Only for https:// addresses: the root certificate of the live site, so the
// button only ever talks to the real site. For a site using Let's Encrypt,
// paste "ISRG Root X1" from https://letsencrypt.org/certificates/ between the
// lines below (all of it, from -----BEGIN to END-----). You can paste more
// than one certificate one after the other. Leave it empty for http:// testing.
const char *ROOT_CA = R"CERT(
)CERT";
