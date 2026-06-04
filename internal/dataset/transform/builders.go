//go:build integration || datasetgen

// Package transform provides gocv-based image transformation functions.
// Each function signature is: func(base, peer []byte) ([]byte, error)
// where peer is the cyclic-pair image used only by the different_image builder.
//
// Build tags:
//   - integration: used by the in-process matrix test
//   - datasetgen:  used by cmd/datasetgen
package transform

import (
	"fmt"
	"image"
	"image/color"
	"math/rand"

	"gocv.io/x/gocv"
)

// Builder is the function type applied to produce a variant byte slice.
// peer is the next-base in cyclic order; most builders ignore it.
type Builder func(base, peer []byte) ([]byte, error)

// BuilderFor returns the Builder for the given Entry, or an error if none exists.
func BuilderFor(e Entry) (Builder, error) {
	switch e.Family {
	case "jpeg_recompress":
		q := intParam(e.Params, "quality", 70)
		return func(base, _ []byte) ([]byte, error) { return recompress(base, q) }, nil

	case "downscale", "upscale":
		scale := float64Param(e.Params, "scale", 1.0)
		q := intParam(e.Params, "quality", 70)
		return func(base, _ []byte) ([]byte, error) { return rescale(base, scale, q) }, nil

	case "format_change":
		format := stringParam(e.Params, "format", "jpeg")
		return func(base, _ []byte) ([]byte, error) { return convertFormat(base, format) }, nil

	case "rotate_cardinal", "rotate_small":
		deg := intParam(e.Params, "degrees", 90)
		return func(base, _ []byte) ([]byte, error) { return rotateImage(base, deg) }, nil

	case "crop_border":
		frac := float64Param(e.Params, "margin_frac", 0.05)
		return func(base, _ []byte) ([]byte, error) { return cropBorder(base, frac) }, nil

	case "brightness":
		pct := intParam(e.Params, "pct", 5)
		return func(base, _ []byte) ([]byte, error) { return adjustBrightness(base, pct) }, nil

	case "noise_light":
		sigma := float64Param(e.Params, "sigma", 5)
		return func(base, _ []byte) ([]byte, error) { return addNoise(base, sigma) }, nil

	case "sharpen":
		return func(base, _ []byte) ([]byte, error) { return sharpenImage(base) }, nil

	case "whatsapp_like":
		maxLong := intParam(e.Params, "max_long_px", 960)
		q := intParam(e.Params, "quality", 40)
		return func(base, _ []byte) ([]byte, error) { return whatsappLike(base, maxLong, q) }, nil

	case "p3_as_srgb":
		q := intParam(e.Params, "quality", 70)
		return func(base, _ []byte) ([]byte, error) { return p3AsRGB(base, q) }, nil

	case "grayscale":
		return func(base, _ []byte) ([]byte, error) { return toGrayscale(base) }, nil

	case "sepia":
		return func(base, _ []byte) ([]byte, error) { return toSepia(base) }, nil

	case "hue_shift":
		deg := intParam(e.Params, "degrees", 30)
		return func(base, _ []byte) ([]byte, error) { return hueShift(base, deg) }, nil

	case "saturation_boost":
		mul := float64Param(e.Params, "multiplier", 1.5)
		return func(base, _ []byte) ([]byte, error) { return saturationBoost(base, mul) }, nil

	case "color_invert":
		return func(base, _ []byte) ([]byte, error) { return invertColors(base) }, nil

	case "localized_recolor":
		frac := float64Param(e.Params, "region_frac", 0.10)
		return func(base, _ []byte) ([]byte, error) { return localizedRecolor(base, frac) }, nil

	case "content_overlay":
		frac := float64Param(e.Params, "coverage_frac", 0.15)
		return func(base, _ []byte) ([]byte, error) { return overlayRectangle(base, frac) }, nil

	case "heavy_crop":
		keepFrac := float64Param(e.Params, "keep_frac", 0.5)
		return func(base, _ []byte) ([]byte, error) { return heavyCrop(base, keepFrac) }, nil

	case "different_image":
		return func(_, peer []byte) ([]byte, error) { return peer, nil }, nil

	default:
		return nil, fmt.Errorf("no builder for family %q", e.Family)
	}
}

// --- gocv helpers ---

func decodeBGR(b []byte) (gocv.Mat, error) {
	m, err := gocv.IMDecode(b, gocv.IMReadColor)
	if err != nil || m.Empty() {
		return gocv.NewMat(), fmt.Errorf("decode: %w", err)
	}
	return m, nil
}

func encodeJPEG(m gocv.Mat, quality int) ([]byte, error) {
	buf, err := gocv.IMEncodeWithParams(gocv.JPEGFileExt, m, []int{gocv.IMWriteJpegQuality, quality})
	if err != nil {
		return nil, err
	}
	defer buf.Close()
	out := make([]byte, len(buf.GetBytes()))
	copy(out, buf.GetBytes())
	return out, nil
}

func recompress(b []byte, q int) ([]byte, error) {
	m, err := decodeBGR(b)
	if err != nil {
		return nil, err
	}
	defer m.Close()
	return encodeJPEG(m, q)
}

func rescale(b []byte, scale float64, q int) ([]byte, error) {
	m, err := decodeBGR(b)
	if err != nil {
		return nil, err
	}
	defer m.Close()

	var w, h int
	if scale < 0 {
		// Negative scale is interpreted as "cap the long side at |scale| pixels".
		maxLong := int(-scale)
		long := m.Cols()
		if m.Rows() > long {
			long = m.Rows()
		}
		if long <= maxLong {
			return encodeJPEG(m, q)
		}
		ratio := float64(maxLong) / float64(long)
		w = int(float64(m.Cols()) * ratio)
		h = int(float64(m.Rows()) * ratio)
	} else {
		w = int(float64(m.Cols()) * scale)
		h = int(float64(m.Rows()) * scale)
	}
	if w < 1 {
		w = 1
	}
	if h < 1 {
		h = 1
	}

	dst := gocv.NewMat()
	defer dst.Close()
	interp := gocv.InterpolationArea
	if scale > 1 {
		interp = gocv.InterpolationLinear
	}
	gocv.Resize(m, &dst, image.Point{X: w, Y: h}, 0, 0, interp)
	return encodeJPEG(dst, q)
}

func convertFormat(b []byte, format string) ([]byte, error) {
	m, err := decodeBGR(b)
	if err != nil {
		return nil, err
	}
	defer m.Close()

	var ext string
	switch format {
	case "png":
		ext = ".png"
	case "gif":
		// gocv does not encode GIF; re-encode as PNG as a proxy for format change.
		ext = ".png"
	case "bmp":
		ext = ".bmp"
	case "tiff":
		ext = ".tiff"
	case "webp":
		ext = ".webp"
	default:
		return encodeJPEG(m, 90)
	}

	buf, err := gocv.IMEncode(gocv.FileExt(ext), m)
	if err != nil {
		return nil, err
	}
	defer buf.Close()
	out := make([]byte, len(buf.GetBytes()))
	copy(out, buf.GetBytes())
	return out, nil
}

func rotateImage(b []byte, deg int) ([]byte, error) {
	m, err := decodeBGR(b)
	if err != nil {
		return nil, err
	}
	defer m.Close()

	switch deg {
	case 90:
		dst := gocv.NewMat()
		defer dst.Close()
		gocv.Rotate(m, &dst, gocv.Rotate90Clockwise)
		return encodeJPEG(dst, 90)
	case 180:
		dst := gocv.NewMat()
		defer dst.Close()
		gocv.Rotate(m, &dst, gocv.Rotate180Clockwise)
		return encodeJPEG(dst, 90)
	case 270:
		dst := gocv.NewMat()
		defer dst.Close()
		gocv.Rotate(m, &dst, gocv.Rotate90CounterClockwise)
		return encodeJPEG(dst, 90)
	default:
		// Arbitrary angle: use warpAffine with border replicate.
		h, w := m.Rows(), m.Cols()
		center := image.Point{X: w / 2, Y: h / 2}
		rot := gocv.GetRotationMatrix2D(center, float64(deg), 1.0)
		defer rot.Close()
		dst := gocv.NewMat()
		defer dst.Close()
		gocv.WarpAffineWithParams(m, &dst, rot,
			image.Point{X: w, Y: h},
			gocv.InterpolationLinear,
			gocv.BorderReplicate,
			color.RGBA{})
		return encodeJPEG(dst, 90)
	}
}

func cropBorder(b []byte, frac float64) ([]byte, error) {
	m, err := decodeBGR(b)
	if err != nil {
		return nil, err
	}
	defer m.Close()

	w, h := m.Cols(), m.Rows()
	dx := int(float64(w) * frac)
	dy := int(float64(h) * frac)
	rect := image.Rect(dx, dy, w-dx, h-dy)
	region := m.Region(rect)
	defer region.Close()
	clone := region.Clone()
	defer clone.Close()
	return encodeJPEG(clone, 85)
}

func adjustBrightness(b []byte, pct int) ([]byte, error) {
	m, err := decodeBGR(b)
	if err != nil {
		return nil, err
	}
	defer m.Close()

	delta := float64(pct) / 100.0 * 255.0
	dst := gocv.NewMat()
	defer dst.Close()
	m.ConvertToWithParams(&dst, gocv.MatTypeCV32F, 1.0, delta)
	out := gocv.NewMat()
	defer out.Close()
	dst.ConvertTo(&out, gocv.MatTypeCV8UC3)
	return encodeJPEG(out, 85)
}

func addNoise(b []byte, sigma float64) ([]byte, error) {
	m, err := decodeBGR(b)
	if err != nil {
		return nil, err
	}
	defer m.Close()

	noise := gocv.NewMatWithSize(m.Rows(), m.Cols(), gocv.MatTypeCV32FC3)
	defer noise.Close()
	// Fill with Gaussian noise using randn.
	gocv.Randn(&noise, gocv.NewScalar(0, 0, 0, 0), gocv.NewScalar(sigma, sigma, sigma, 0))

	f := gocv.NewMat()
	defer f.Close()
	m.ConvertTo(&f, gocv.MatTypeCV32FC3)
	gocv.Add(f, noise, &f)
	out := gocv.NewMat()
	defer out.Close()
	f.ConvertTo(&out, gocv.MatTypeCV8UC3)
	return encodeJPEG(out, 90)
}

func sharpenImage(b []byte) ([]byte, error) {
	m, err := decodeBGR(b)
	if err != nil {
		return nil, err
	}
	defer m.Close()

	blurred := gocv.NewMat()
	defer blurred.Close()
	gocv.GaussianBlur(m, &blurred, image.Point{X: 0, Y: 0}, 2.0, 0, gocv.BorderDefault)

	dst := gocv.NewMat()
	defer dst.Close()
	gocv.AddWeighted(m, 1.5, blurred, -0.5, 0, &dst)
	return encodeJPEG(dst, 85)
}

func whatsappLike(b []byte, maxLong, q int) ([]byte, error) {
	m, err := decodeBGR(b)
	if err != nil {
		return nil, err
	}
	defer m.Close()

	long := m.Cols()
	if m.Rows() > long {
		long = m.Rows()
	}
	if long <= maxLong {
		return encodeJPEG(m, q)
	}
	scale := float64(maxLong) / float64(long)
	dst := gocv.NewMat()
	defer dst.Close()
	gocv.Resize(m, &dst, image.Point{X: int(float64(m.Cols()) * scale), Y: int(float64(m.Rows()) * scale)}, 0, 0, gocv.InterpolationArea)
	return encodeJPEG(dst, q)
}

// p3AsRGB simulates a Display P3 photo stripped of its ICC profile and
// interpreted as sRGB. Applies the sRGB→Display P3 primaries matrix to pixel
// values to simulate the colour shift a CMS-unaware pipeline introduces.
func p3AsRGB(b []byte, q int) ([]byte, error) {
	m, err := decodeBGR(b)
	if err != nil {
		return nil, err
	}
	defer m.Close()

	// sRGB → linearise → apply approx primaries matrix → re-gamma
	// Approximate 3×3 matrix (row-major, BGR order):
	//   [ 0.9505  0.0000  0.0000 ]   (B channel: slight shift)
	//   [ 0.0000  0.9642  0.0000 ]   (G channel)
	//   [ 0.0000  0.0000  1.0890 ]   (R channel: most shift for reds)
	// This is an approximation of the D50-adapted sRGB→P3 difference.
	kernel := [3]float32{0.9505, 0.9642, 1.0890} // per-channel multipliers (B, G, R)

	f := gocv.NewMat()
	defer f.Close()
	m.ConvertTo(&f, gocv.MatTypeCV32FC3)

	channels := gocv.Split(f)
	for i := 0; i < 3; i++ {
		scaled := gocv.NewMat()
		channels[i].ConvertToWithParams(&scaled, gocv.MatTypeCV32F, float32(kernel[i]), 0)
		channels[i].Close()
		channels[i] = scaled
	}
	merged := gocv.NewMat()
	defer merged.Close()
	gocv.Merge(channels, &merged)
	for _, c := range channels {
		c.Close()
	}
	out := gocv.NewMat()
	defer out.Close()
	merged.ConvertTo(&out, gocv.MatTypeCV8UC3)
	return encodeJPEG(out, q)
}

func toGrayscale(b []byte) ([]byte, error) {
	m, err := decodeBGR(b)
	if err != nil {
		return nil, err
	}
	defer m.Close()

	gray := gocv.NewMat()
	defer gray.Close()
	gocv.CvtColor(m, &gray, gocv.ColorBGRToGray)
	bgr := gocv.NewMat()
	defer bgr.Close()
	gocv.CvtColor(gray, &bgr, gocv.ColorGrayToBGR)
	return encodeJPEG(bgr, 85)
}

func toSepia(b []byte) ([]byte, error) {
	m, err := decodeBGR(b)
	if err != nil {
		return nil, err
	}
	defer m.Close()

	f := gocv.NewMat()
	defer f.Close()
	m.ConvertTo(&f, gocv.MatTypeCV32FC3)

	channels := gocv.Split(f)
	b0 := channels[0] // B
	g0 := channels[1] // G
	r0 := channels[2] // R

	// Sepia: outR = clamp(R*0.393 + G*0.769 + B*0.189)
	//        outG = clamp(R*0.349 + G*0.686 + B*0.168)
	//        outB = clamp(R*0.272 + G*0.534 + B*0.131)
	newR := gocv.NewMat()
	newG := gocv.NewMat()
	newB := gocv.NewMat()
	tmp := gocv.NewMat()

	r0.ConvertToWithParams(&newR, gocv.MatTypeCV32F, 0.393, 0)
	g0.ConvertToWithParams(&tmp, gocv.MatTypeCV32F, 0.769, 0)
	gocv.Add(newR, tmp, &newR)
	b0.ConvertToWithParams(&tmp, gocv.MatTypeCV32F, 0.189, 0)
	gocv.Add(newR, tmp, &newR)

	r0.ConvertToWithParams(&newG, gocv.MatTypeCV32F, 0.349, 0)
	g0.ConvertToWithParams(&tmp, gocv.MatTypeCV32F, 0.686, 0)
	gocv.Add(newG, tmp, &newG)
	b0.ConvertToWithParams(&tmp, gocv.MatTypeCV32F, 0.168, 0)
	gocv.Add(newG, tmp, &newG)

	r0.ConvertToWithParams(&newB, gocv.MatTypeCV32F, 0.272, 0)
	g0.ConvertToWithParams(&tmp, gocv.MatTypeCV32F, 0.534, 0)
	gocv.Add(newB, tmp, &newB)
	b0.ConvertToWithParams(&tmp, gocv.MatTypeCV32F, 0.131, 0)
	gocv.Add(newB, tmp, &newB)

	for _, c := range channels {
		c.Close()
	}
	tmp.Close()

	merged := gocv.NewMat()
	defer merged.Close()
	gocv.Merge([]gocv.Mat{newB, newG, newR}, &merged)
	newR.Close()
	newG.Close()
	newB.Close()

	out := gocv.NewMat()
	defer out.Close()
	merged.ConvertTo(&out, gocv.MatTypeCV8UC3)
	return encodeJPEG(out, 85)
}

func hueShift(b []byte, deg int) ([]byte, error) {
	m, err := decodeBGR(b)
	if err != nil {
		return nil, err
	}
	defer m.Close()

	hsv := gocv.NewMat()
	defer hsv.Close()
	gocv.CvtColor(m, &hsv, gocv.ColorBGRToHSV)

	channels := gocv.Split(hsv)
	// OpenCV HSV: H in [0,180], so 1° of hue = 0.5 units.
	hShift := float32(deg) / 2.0
	newH := gocv.NewMat()
	channels[0].ConvertToWithParams(&newH, gocv.MatTypeCV8U, 1.0, float32(hShift))
	channels[0].Close()
	channels[0] = newH

	hsvOut := gocv.NewMat()
	defer hsvOut.Close()
	gocv.Merge(channels, &hsvOut)
	for _, c := range channels {
		c.Close()
	}
	out := gocv.NewMat()
	defer out.Close()
	gocv.CvtColor(hsvOut, &out, gocv.ColorHSVToBGR)
	return encodeJPEG(out, 85)
}

func saturationBoost(b []byte, mul float64) ([]byte, error) {
	m, err := decodeBGR(b)
	if err != nil {
		return nil, err
	}
	defer m.Close()

	hsv := gocv.NewMat()
	defer hsv.Close()
	gocv.CvtColor(m, &hsv, gocv.ColorBGRToHSV)

	channels := gocv.Split(hsv)
	newS := gocv.NewMat()
	channels[1].ConvertToWithParams(&newS, gocv.MatTypeCV8U, float32(mul), 0)
	channels[1].Close()
	channels[1] = newS

	hsvOut := gocv.NewMat()
	defer hsvOut.Close()
	gocv.Merge(channels, &hsvOut)
	for _, c := range channels {
		c.Close()
	}
	out := gocv.NewMat()
	defer out.Close()
	gocv.CvtColor(hsvOut, &out, gocv.ColorHSVToBGR)
	return encodeJPEG(out, 85)
}

func invertColors(b []byte) ([]byte, error) {
	m, err := decodeBGR(b)
	if err != nil {
		return nil, err
	}
	defer m.Close()

	inv := gocv.NewMat()
	defer inv.Close()
	gocv.BitwiseNot(m, &inv)
	return encodeJPEG(inv, 85)
}

// localizedRecolor recolours a region occupying ~regionFrac of the image area.
// It shifts the hue of that rectangle by 120°, simulating object recolour.
func localizedRecolor(b []byte, regionFrac float64) ([]byte, error) {
	m, err := decodeBGR(b)
	if err != nil {
		return nil, err
	}
	defer m.Close()

	w, h := m.Cols(), m.Rows()
	side := int(float64(w*h) * regionFrac)
	s := 1
	for s*s < side {
		s++
	}
	if s > w/2 {
		s = w / 2
	}
	if s > h/2 {
		s = h / 2
	}
	x0 := int(float64(w) * 0.30)
	y0 := int(float64(h) * 0.30)

	clone := m.Clone()
	defer clone.Close()

	region := clone.Region(image.Rect(x0, y0, x0+s, y0+s))
	defer region.Close()

	// Hue-shift the region in place.
	hsv := gocv.NewMat()
	defer hsv.Close()
	gocv.CvtColor(region, &hsv, gocv.ColorBGRToHSV)
	channels := gocv.Split(hsv)
	newH := gocv.NewMat()
	channels[0].ConvertToWithParams(&newH, gocv.MatTypeCV8U, 1.0, 60) // +120° = +60 in [0,180]
	channels[0].Close()
	channels[0] = newH
	hsvOut := gocv.NewMat()
	defer hsvOut.Close()
	gocv.Merge(channels, &hsvOut)
	for _, c := range channels {
		c.Close()
	}
	gocv.CvtColor(hsvOut, &region, gocv.ColorHSVToBGR)

	return encodeJPEG(clone, 85)
}

func overlayRectangle(b []byte, coverageFrac float64) ([]byte, error) {
	m, err := decodeBGR(b)
	if err != nil {
		return nil, err
	}
	defer m.Close()

	w, h := m.Cols(), m.Rows()
	area := int(float64(w*h) * coverageFrac)
	s := 1
	for s*s < area {
		s++
	}
	if s > w/2 {
		s = w / 2
	}
	if s > h/2 {
		s = h / 2
	}
	x0 := int(float64(w) * 0.05)
	y0 := int(float64(h) * 0.05)

	clone := m.Clone()
	defer clone.Close()
	gocv.Rectangle(&clone, image.Rect(x0, y0, x0+s, y0+s), color.RGBA{255, 0, 255, 0}, -1)
	return encodeJPEG(clone, 85)
}

// heavyCrop keeps keepFrac of the image, centred.
func heavyCrop(b []byte, keepFrac float64) ([]byte, error) {
	m, err := decodeBGR(b)
	if err != nil {
		return nil, err
	}
	defer m.Close()

	w, h := m.Cols(), m.Rows()
	newW := int(float64(w) * keepFrac)
	newH := int(float64(h) * keepFrac)
	x0 := (w - newW) / 2
	y0 := (h - newH) / 2
	region := m.Region(image.Rect(x0, y0, x0+newW, y0+newH))
	defer region.Close()
	clone := region.Clone()
	defer clone.Close()
	return encodeJPEG(clone, 85)
}

// --- param helpers ---

func intParam(p map[string]any, key string, def int) int {
	v, ok := p[key]
	if !ok {
		return def
	}
	switch x := v.(type) {
	case int:
		return x
	case float64:
		return int(x)
	}
	return def
}

func float64Param(p map[string]any, key string, def float64) float64 {
	v, ok := p[key]
	if !ok {
		return def
	}
	switch x := v.(type) {
	case float64:
		return x
	case int:
		return float64(x)
	}
	return def
}

func stringParam(p map[string]any, key, def string) string {
	v, ok := p[key]
	if !ok {
		return def
	}
	if s, ok := v.(string); ok {
		return s
	}
	return def
}

// randSource is a module-level rand for noise; seeded per-call to avoid test
// non-determinism. Tests that need reproducibility should seed externally.
var randSource = rand.New(rand.NewSource(42)) //nolint:gosec
