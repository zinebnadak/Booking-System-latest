package handlers

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// phoneJPEG is a w×h photo with a red top-left corner and a "rotate me"
// note (EXIF orientation) plus a made-up GPS note, like a phone photo.
func phoneJPEG(t *testing.T, w, h, orientation int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := color.RGBA{30, 120, 200, 255}
			if x < w/4 && y < h/4 {
				c = color.RGBA{230, 20, 20, 255}
			}
			img.SetRGBA(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	// EXIF: II*, IFD0 with orientation and a GPS pointer.
	tiff := []byte("II*\x00\x08\x00\x00\x00")
	ifd := make([]byte, 2+2*12+4)
	binary.LittleEndian.PutUint16(ifd, 2)
	binary.LittleEndian.PutUint16(ifd[2:], 0x0112)
	binary.LittleEndian.PutUint16(ifd[4:], 3)
	binary.LittleEndian.PutUint32(ifd[6:], 1)
	binary.LittleEndian.PutUint16(ifd[10:], uint16(orientation))
	binary.LittleEndian.PutUint16(ifd[14:], 0x8825) // GPS
	binary.LittleEndian.PutUint16(ifd[16:], 4)
	binary.LittleEndian.PutUint32(ifd[18:], 1)
	binary.LittleEndian.PutUint32(ifd[22:], 0x60)
	app1 := append([]byte("Exif\x00\x00"), append(tiff, ifd...)...)
	seg := []byte{0xFF, 0xE1, 0, 0}
	binary.BigEndian.PutUint16(seg[2:], uint16(len(app1)+2))
	raw := buf.Bytes()
	out := append([]byte{0xFF, 0xD8}, seg...)
	out = append(out, app1...)
	return append(out, raw[2:]...)
}

func uploadPhoto(t *testing.T, e *env, id string, name string, data []byte) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("photo", name)
	fw.Write(data)
	mw.Close()
	req := httptest.NewRequest("POST", "http://example.test/admin/menu/"+id+"/photo/upload", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: e.srv.makeSession(roleManager, e.srv.Now())})
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

func TestDishPhotos(t *testing.T) {
	e := newEnv(t, nil)
	lax := menuID(t, e, "Laxfilé")
	// A phone photo taken sideways: 3000 wide, 2000 high, "turn me clockwise".
	if rec := uploadPhoto(t, e, lax, "IMG_0001.JPG", phoneJPEG(t, 3000, 2000, 6)); !strings.Contains(loc(rec), "msg=photosaved") {
		t.Fatalf("upload: %d %s", rec.Code, loc(rec))
	}
	item, _ := e.store.MenuItem(atoiDefault(lax, 0))
	if !photoName.MatchString(item.Photo) {
		t.Fatalf("photo name %q", item.Photo)
	}
	saved, err := os.ReadFile(filepath.Join(e.srv.photoDir(), item.Photo))
	if err != nil {
		t.Fatal(err)
	}
	img, err := jpeg.Decode(bytes.NewReader(saved))
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() != 800 || b.Dy() != 1200 {
		t.Fatalf("saved %dx%d, want upright 800x1200", b.Dx(), b.Dy())
	}
	// Turned clockwise, the red corner is now top-right.
	if r, _, _, _ := img.At(790, 10).RGBA(); r>>8 < 180 {
		t.Fatal("photo wasn't turned upright")
	}
	if bytes.Contains(saved, []byte("Exif")) {
		t.Fatal("hidden photo data (EXIF, GPS) kept")
	}
	// Guests see it on their phone menu and on the takeaway page.
	e.expect(e.do("GET", "/takeaway", nil, false), 200, "/photos/"+item.Photo)
	rec := e.do("GET", "/photos/"+item.Photo, nil, false)
	e.expect(rec, 200)
	if rec.Header().Get("Content-Type") != "image/jpeg" {
		t.Fatalf("type %q", rec.Header().Get("Content-Type"))
	}
	// Nothing else can be read through /photos/.
	for _, p := range []string{"/photos/..%2fozmo.json", "/photos/x.jpg", "/photos/1-0123456789abcdef.png"} {
		if rec := e.do("GET", p, nil, false); rec.Code != 404 {
			t.Fatalf("%s: %d", p, rec.Code)
		}
	}
	// A PNG works too; the old photo file is removed.
	var pngBuf bytes.Buffer
	png.Encode(&pngBuf, image.NewRGBA(image.Rect(0, 0, 400, 300)))
	uploadPhoto(t, e, lax, "dish.png", pngBuf.Bytes())
	if _, err := os.Stat(filepath.Join(e.srv.photoDir(), item.Photo)); !os.IsNotExist(err) {
		t.Fatal("old photo file left behind")
	}
	// Not a photo: refused.
	if rec := uploadPhoto(t, e, lax, "menu.pdf", []byte("%PDF-1.4 not a photo")); !strings.Contains(loc(rec), "msg=photobad") {
		t.Fatalf("pdf: %s", loc(rec))
	}
	// Editing the dish keeps its photo; removing it deletes the file.
	item, _ = e.store.MenuItem(atoiDefault(lax, 0))
	e.do("POST", "/admin/menu", url.Values{"id": {lax}, "name": {"Laxfilé"}, "category": {"Huvudrätter"}, "price": {"27,90"}, "available": {"on"}}, true)
	if got, _ := e.store.MenuItem(atoiDefault(lax, 0)); got.Photo != item.Photo {
		t.Fatal("editing the dish lost its photo")
	}
	e.do("POST", "/admin/menu/"+lax+"/photo/remove", url.Values{}, true)
	if _, err := os.Stat(filepath.Join(e.srv.photoDir(), item.Photo)); !os.IsNotExist(err) {
		t.Fatal("removed photo still on disk")
	}
}
