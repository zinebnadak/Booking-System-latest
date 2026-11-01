package handlers

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	_ "image/png" // PNG photos can be uploaded too
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
)

// Dish photos. The manager uploads one per dish on the Menu page; guests
// see it on the phone menu and the takeaway menu.
//
// Every upload is made safe and small before it's kept: it must really be a
// JPEG or PNG, at most 40 megapixels; it's turned upright (phones store many
// photos sideways with a "rotate me" note), shrunk to at most 1200 pixels
// and saved again as JPEG. Saving it again also drops everything hidden in
// the original file, like where the photo was taken.

const (
	maxPhotoUpload = 15 << 20 // 15 MB, a big phone photo
	maxPhotoPixels = 40_000_000
	photoMaxSide   = 1200
)

var photoName = regexp.MustCompile(`^[0-9]+-[0-9a-f]{16}\.jpg$`)

var errNotPhoto = errors.New("not a JPEG or PNG photo")

// photoDir is the photos folder, next to the data file (so it's backed up
// with it and never ends up in git).
func (s *Server) photoDir() string { return filepath.Join(filepath.Dir(s.Store.Path()), "photos") }

// menuPhoto saves a new photo for a dish (or removes it).
func (s *Server) menuPhoto(w http.ResponseWriter, r *http.Request) {
	id := atoiDefault(r.PathValue("id"), 0)
	item, err := s.Store.MenuItem(id)
	if err != nil {
		s.notFound(w, r)
		return
	}
	if r.PathValue("action") == "remove" {
		old, _ := s.Store.SetMenuPhoto(id, "")
		s.removePhoto(old)
		s.live.publish()
		redirectMsg(w, r, "/admin/menu", nil, "photoremoved")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxPhotoUpload+1<<20)
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		redirectMsg(w, r, "/admin/menu", nil, "photobig")
		return
	}
	defer r.MultipartForm.RemoveAll()
	f, _, err := r.FormFile("photo")
	if err != nil {
		redirectMsg(w, r, "/admin/menu", nil, "photonone")
		return
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, maxPhotoUpload+1))
	if err != nil || len(raw) > maxPhotoUpload {
		redirectMsg(w, r, "/admin/menu", nil, "photobig")
		return
	}
	out, err := cleanPhoto(raw)
	if err != nil {
		log.Printf("photo for %q: %v", item.Name, err)
		redirectMsg(w, r, "/admin/menu", nil, "photobad")
		return
	}
	if err := os.MkdirAll(s.photoDir(), 0o755); err != nil {
		log.Printf("photos folder: %v", err)
		redirectMsg(w, r, "/admin/menu", nil, "photobad")
		return
	}
	var rnd [8]byte
	if _, err := rand.Read(rnd[:]); err != nil {
		redirectMsg(w, r, "/admin/menu", nil, "photobad")
		return
	}
	name := strconv.Itoa(id) + "-" + hex.EncodeToString(rnd[:]) + ".jpg"
	if err := os.WriteFile(filepath.Join(s.photoDir(), name), out, 0o644); err != nil {
		log.Printf("save photo: %v", err)
		redirectMsg(w, r, "/admin/menu", nil, "photobad")
		return
	}
	old, err := s.Store.SetMenuPhoto(id, name)
	if err != nil {
		s.removePhoto(name)
		s.notFound(w, r)
		return
	}
	s.removePhoto(old)
	s.live.publish()
	redirectMsg(w, r, "/admin/menu", nil, "photosaved")
}

func (s *Server) removePhoto(name string) {
	if name != "" && photoName.MatchString(name) {
		_ = os.Remove(filepath.Join(s.photoDir(), name))
	}
}

// servePhoto sends a dish photo. A new photo gets a new name, so browsers
// can keep each one for a long time.
func (s *Server) servePhoto(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !photoName.MatchString(name) {
		http.NotFound(w, r)
		return
	}
	raw, err := os.ReadFile(filepath.Join(s.photoDir(), name))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "image/jpeg")
	h.Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Write(raw)
}

// cleanPhoto checks an upload, turns it upright, shrinks it and saves it
// again as a plain JPEG.
func cleanPhoto(raw []byte) ([]byte, error) {
	cfg, format, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil || (format != "jpeg" && format != "png") {
		return nil, errNotPhoto
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width*cfg.Height > maxPhotoPixels {
		return nil, errors.New("photo too large")
	}
	img, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, errNotPhoto
	}
	if format == "jpeg" {
		img = upright(img, jpegOrientation(raw))
	}
	img = shrink(img, photoMaxSide)
	var out bytes.Buffer
	if err := jpeg.Encode(&out, img, &jpeg.Options{Quality: 82}); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// shrink scales an image down so its longest side is at most max pixels,
// averaging the source pixels under each new one (sharp, no jagged edges).
func shrink(src image.Image, max int) image.Image {
	b := src.Bounds()
	sw, sh := b.Dx(), b.Dy()
	dw, dh := sw, sh
	if sw >= sh && sw > max {
		dw, dh = max, sh*max/sw
	} else if sh > sw && sh > max {
		dw, dh = sw*max/sh, max
	}
	if dw < 1 {
		dw = 1
	}
	if dh < 1 {
		dh = 1
	}
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for y := 0; y < dh; y++ {
		y0, y1 := y*sh/dh, (y+1)*sh/dh
		if y1 == y0 {
			y1 = y0 + 1
		}
		for x := 0; x < dw; x++ {
			x0, x1 := x*sw/dw, (x+1)*sw/dw
			if x1 == x0 {
				x1 = x0 + 1
			}
			var r, g, bl, n uint64
			for sy := y0; sy < y1; sy++ {
				for sx := x0; sx < x1; sx++ {
					cr, cg, cb, _ := src.At(b.Min.X+sx, b.Min.Y+sy).RGBA()
					r, g, bl, n = r+uint64(cr), g+uint64(cg), bl+uint64(cb), n+1
				}
			}
			dst.SetRGBA(x, y, color.RGBA{uint8(r / n >> 8), uint8(g / n >> 8), uint8(bl / n >> 8), 255})
		}
	}
	return dst
}

// jpegOrientation reads the "rotate me" note (EXIF orientation, 1–8) that
// phones put in their photos. 1 = already upright.
func jpegOrientation(raw []byte) int {
	if len(raw) < 4 || raw[0] != 0xFF || raw[1] != 0xD8 {
		return 1
	}
	for i := 2; i+4 <= len(raw); {
		if raw[i] != 0xFF {
			return 1
		}
		marker := raw[i+1]
		size := int(binary.BigEndian.Uint16(raw[i+2:]))
		if marker == 0xDA || size < 2 || i+2+size > len(raw) { // image data starts: no note
			return 1
		}
		seg := raw[i+4 : i+2+size]
		if marker == 0xE1 && len(seg) > 14 && string(seg[:6]) == "Exif\x00\x00" {
			return exifOrientation(seg[6:])
		}
		i += 2 + size
	}
	return 1
}

func exifOrientation(tiff []byte) int {
	if len(tiff) < 8 {
		return 1
	}
	var bo binary.ByteOrder
	switch string(tiff[:2]) {
	case "II":
		bo = binary.LittleEndian
	case "MM":
		bo = binary.BigEndian
	default:
		return 1
	}
	ifd := int(bo.Uint32(tiff[4:]))
	if ifd < 8 || ifd+2 > len(tiff) {
		return 1
	}
	n := int(bo.Uint16(tiff[ifd:]))
	for k := 0; k < n; k++ {
		e := ifd + 2 + k*12
		if e+12 > len(tiff) {
			return 1
		}
		if bo.Uint16(tiff[e:]) == 0x0112 {
			if v := int(bo.Uint16(tiff[e+8:])); v >= 1 && v <= 8 {
				return v
			}
			return 1
		}
	}
	return 1
}

// upright turns and/or mirrors an image as its EXIF orientation says.
func upright(src image.Image, o int) image.Image {
	if o <= 1 || o > 8 {
		return src
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	dw, dh := w, h
	if o >= 5 {
		dw, dh = h, w
	}
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var dx, dy int
			switch o {
			case 2: // mirrored
				dx, dy = w-1-x, y
			case 3: // upside down
				dx, dy = w-1-x, h-1-y
			case 4: // mirrored upside down
				dx, dy = x, h-1-y
			case 5: // mirrored, turned
				dx, dy = y, x
			case 6: // turned: needs a quarter turn clockwise
				dx, dy = h-1-y, x
			case 7: // mirrored, turned the other way
				dx, dy = h-1-y, w-1-x
			case 8: // needs a quarter turn anticlockwise
				dx, dy = y, w-1-x
			}
			dst.Set(dx, dy, src.At(b.Min.X+x, b.Min.Y+y))
		}
	}
	return dst
}
