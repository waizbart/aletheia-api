//go:build e2e

package e2e_test

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"net/http"
	"strings"
	"testing"
)

// --- Certify -----------------------------------------------------------------

func TestE2E_Certify_Image_HappyPath(t *testing.T) {
	env := setupE2E(t)
	defer env.cleanup()

	body := loadTestdata(t, "aletheia.jpg")
	wantHash := sha256Hex(body)

	status, respBody := postCertify(t, env, "aletheia.jpg", "image/jpeg", body, "alice")
	if status != http.StatusCreated {
		t.Fatalf("status = %d body=%s, want 201", status, respBody)
	}

	cert := decodeCert(t, respBody)
	if cert.ContentHash != wantHash {
		t.Errorf("ContentHash = %s, want %s", cert.ContentHash, wantHash)
	}
	if cert.Registrant != "alice" {
		t.Errorf("Registrant = %s, want alice", cert.Registrant)
	}
	if cert.TxHash == "" {
		t.Error("TxHash empty")
	}
	if cert.ID == "" {
		t.Error("ID empty")
	}

	phashLen, descLen, kpLen, blobKey := assertRowExists(t, env.db, wantHash)
	if phashLen != 32 {
		t.Errorf("phash bytes = %d, want 32", phashLen)
	}
	if descLen == 0 {
		t.Error("orb_descriptors empty")
	}
	if descLen%32 != 0 {
		t.Errorf("orb_descriptors length %d not a multiple of 32", descLen)
	}
	if kpLen == 0 {
		t.Error("orb_keypoints empty")
	}
	if kpLen%48 != 0 {
		t.Errorf("orb_keypoints length %d not a multiple of 48", kpLen)
	}
	if blobKey == "" {
		t.Error("image_blob_key empty")
	}
	assertBlobExists(t, env, blobKey)
}

func TestE2E_Certify_Duplicate_Returns409(t *testing.T) {
	env := setupE2E(t)
	defer env.cleanup()

	body := loadTestdata(t, "aletheia.jpg")
	if status, _ := postCertify(t, env, "a.jpg", "image/jpeg", body, "alice"); status != http.StatusCreated {
		t.Fatalf("first certify status = %d, want 201", status)
	}

	status, respBody := postCertify(t, env, "a.jpg", "image/jpeg", body, "bob")
	if status != http.StatusConflict {
		t.Fatalf("duplicate status = %d body=%s, want 409", status, respBody)
	}
	if !strings.Contains(string(respBody), "already certified") {
		t.Errorf("body should mention already certified: %s", respBody)
	}

	if n := countCertificates(t, env.db); n != 1 {
		t.Errorf("certificates count = %d, want 1", n)
	}
}

func TestE2E_Certify_NonImage_DegradedSave(t *testing.T) {
	env := setupE2E(t)
	defer env.cleanup()

	body := []byte("this is plain text wearing an image content-type")
	wantHash := sha256Hex(body)

	status, respBody := postCertify(t, env, "fake.jpg", "image/jpeg", body, "carol")
	if status != http.StatusCreated {
		t.Fatalf("status = %d body=%s, want 201", status, respBody)
	}

	phashLen, descLen, kpLen, blobKey := assertRowExists(t, env.db, wantHash)
	if phashLen != 0 {
		t.Errorf("expected nil phash for non-image, got %d bytes", phashLen)
	}
	if descLen != 0 {
		t.Errorf("expected no descriptors, got %d bytes", descLen)
	}
	if kpLen != 0 {
		t.Errorf("expected no keypoints, got %d bytes", kpLen)
	}
	if blobKey != "" {
		t.Errorf("expected empty blob key for non-image, got %q", blobKey)
	}
}

func TestE2E_Certify_Image_TooSparse_DegradedSave(t *testing.T) {
	env := setupE2E(t)
	defer env.cleanup()

	body := solidColorJPEG(t, 320, 240, color.RGBA{R: 128, G: 128, B: 128, A: 255})
	wantHash := sha256Hex(body)

	status, _ := postCertify(t, env, "flat.jpg", "image/jpeg", body, "dan")
	if status != http.StatusCreated {
		t.Fatalf("status = %d, want 201", status)
	}

	phashLen, descLen, _, blobKey := assertRowExists(t, env.db, wantHash)
	if phashLen != 32 {
		t.Errorf("expected pHash on the flat image, got len=%d", phashLen)
	}
	if descLen != 0 {
		t.Logf("note: ORB found no descriptors on solid color (expected on textureless input); blob_key=%q", blobKey)
	}
}

func TestE2E_Certify_MissingFile(t *testing.T) {
	env := setupE2E(t)
	defer env.cleanup()

	req, err := http.NewRequest(http.MethodPost, env.server.URL+"/certificates", nil)
	if err != nil {
		t.Fatalf("new req: %v", err)
	}
	status, _ := doRequest(t, req)
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", status)
	}
}

func TestE2E_Certify_UnsupportedMediaType(t *testing.T) {
	env := setupE2E(t)
	defer env.cleanup()

	status, _ := postCertify(t, env, "evil.exe", "application/x-msdownload", []byte("MZ"), "")
	if status != http.StatusUnsupportedMediaType {
		t.Fatalf("status = %d, want 415", status)
	}
}

// --- Verify by hash ----------------------------------------------------------

func TestE2E_VerifyByHash_Found(t *testing.T) {
	env := setupE2E(t)
	defer env.cleanup()

	body := loadTestdata(t, "aletheia.jpg")
	if status, _ := postCertify(t, env, "a.jpg", "image/jpeg", body, "alice"); status != http.StatusCreated {
		t.Fatalf("certify status = %d", status)
	}

	hash := sha256Hex(body)
	status, respBody := getVerifyHash(t, env, hash)
	if status != http.StatusOK {
		t.Fatalf("status = %d body=%s, want 200", status, respBody)
	}
	v := decodeVerify(t, respBody)
	if !v.Certified {
		t.Error("expected certified=true")
	}
	if v.Certificate == nil || v.Certificate.ContentHash != hash {
		t.Errorf("certificate mismatch: %+v", v.Certificate)
	}
}

func TestE2E_VerifyByHash_NotFound(t *testing.T) {
	env := setupE2E(t)
	defer env.cleanup()

	status, respBody := getVerifyHash(t, env, strings.Repeat("0", 64))
	if status != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", status)
	}
	v := decodeVerify(t, respBody)
	if v.Certified {
		t.Error("expected certified=false")
	}
	if v.Certificate != nil {
		t.Errorf("expected nil certificate, got %+v", v.Certificate)
	}
}

func TestE2E_VerifyByHash_MissingParam(t *testing.T) {
	env := setupE2E(t)
	defer env.cleanup()

	status, _ := getVerifyHash(t, env, "")
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", status)
	}
}

// --- Verify by file (perceptual) --------------------------------------------

func TestE2E_VerifyByFile_PerceptualMatrix(t *testing.T) {
	env := setupE2E(t)
	defer env.cleanup()

	ref := loadTestdata(t, "aletheia.jpg")
	if status, _ := postCertify(t, env, "a.jpg", "image/jpeg", ref, "alice"); status != http.StatusCreated {
		t.Fatalf("seed certify status = %d", status)
	}

	matrix := []struct {
		file string
		mime string
		want bool
	}{
		{"aletheia.jpg", "image/jpeg", true},                // exact bytes (SHA hits)
		{"aletheia.png", "image/png", true},                 // PNG re-encoded
		{"aletheia.gif", "image/gif", true},                 // GIF format
		{"aletheia-q10.jpg", "image/jpeg", true},            // worst JPEG quality
		{"aletheia-q20.jpg", "image/jpeg", true},
		{"aletheia-q30.jpg", "image/jpeg", true},
		{"aletheia-q40.jpg", "image/jpeg", true},
		{"aletheia-q50.jpg", "image/jpeg", true},
		{"aletheia-q60.jpg", "image/jpeg", true},
		{"aletheia-q70.jpg", "image/jpeg", true},
		{"aletheia-q80.jpg", "image/jpeg", true},            // near-original quality
		{"aletheia-rotated-90.jpg", "image/jpeg", true},     // rotation handled by pHash variants + ORB
		{"aletheia-rotated-180.jpg", "image/jpeg", true},
		{"aletheia-rotated-270.jpg", "image/jpeg", true},
		{"aletheia-cropped-10p.jpg", "image/jpeg", true},    // crop within ORB tolerance
		{"aletheia-changed-1.jpg", "image/jpeg", false},     // overlay (max LAB > 38)
		{"aletheia-changed-2.jpg", "image/jpeg", false},     // small dots, the fragile case
		{"aletheia-changed-3.jpg", "image/jpeg", false},
		{"aletheia-changed-4.jpg", "image/jpeg", false},
		{"aletheia-changed-5.jpg", "image/jpeg", false},
		{"aletheia-changed-6.jpg", "image/jpeg", false},
		{"aletheia-changed-7.jpg", "image/jpeg", false},
		{"aletheia-filter-1.jpg", "image/jpeg", false},      // global filter (mean LAB > 12)
		{"aletheia-filter-2.jpg", "image/jpeg", false},
		{"aletheia-filter-3.jpg", "image/jpeg", false},
	}

	for _, tc := range matrix {
		tc := tc
		t.Run(tc.file, func(t *testing.T) {
			body := loadTestdata(t, tc.file)
			status, respBody := postVerifyFile(t, env, tc.file, tc.mime, body)

			wantStatus := http.StatusOK
			if !tc.want {
				wantStatus = http.StatusNotFound
			}
			if status != wantStatus {
				t.Fatalf("status = %d body=%s, want %d", status, respBody, wantStatus)
			}
			v := decodeVerify(t, respBody)
			if v.Certified != tc.want {
				t.Errorf("certified = %v, want %v body=%s", v.Certified, tc.want, respBody)
			}
		})
	}
}

func TestE2E_VerifyByFile_UnrelatedImage_NotCertified(t *testing.T) {
	env := setupE2E(t)
	defer env.cleanup()

	if status, _ := postCertify(t, env, "ref.jpg", "image/jpeg", loadTestdata(t, "aletheia.jpg"), "alice"); status != http.StatusCreated {
		t.Fatalf("seed certify status = %d", status)
	}

	other := patternedJPEG(t, 512, 512)
	status, respBody := postVerifyFile(t, env, "other.jpg", "image/jpeg", other)
	if status != http.StatusNotFound {
		t.Fatalf("status = %d body=%s, want 404", status, respBody)
	}
	v := decodeVerify(t, respBody)
	if v.Certified {
		t.Errorf("expected certified=false for unrelated image, body=%s", respBody)
	}
}

func TestE2E_VerifyByFile_NonImageBytes_NotCertified(t *testing.T) {
	env := setupE2E(t)
	defer env.cleanup()

	if status, _ := postCertify(t, env, "ref.jpg", "image/jpeg", loadTestdata(t, "aletheia.jpg"), "alice"); status != http.StatusCreated {
		t.Fatalf("seed certify status = %d", status)
	}

	status, respBody := postVerifyFile(t, env, "fake.jpg", "image/jpeg", []byte("absolutely not an image"))
	if status != http.StatusNotFound {
		t.Fatalf("status = %d body=%s, want 404", status, respBody)
	}
}

func TestE2E_VerifyByFile_NoCertificatesYet(t *testing.T) {
	env := setupE2E(t)
	defer env.cleanup()

	body := loadTestdata(t, "aletheia.jpg")
	status, _ := postVerifyFile(t, env, "a.jpg", "image/jpeg", body)
	if status != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", status)
	}
}

func TestE2E_VerifyByFile_MissingFile(t *testing.T) {
	env := setupE2E(t)
	defer env.cleanup()

	req, err := http.NewRequest(http.MethodPost, env.server.URL+"/certificates/verify", nil)
	if err != nil {
		t.Fatalf("new req: %v", err)
	}
	status, _ := doRequest(t, req)
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", status)
	}
}

func TestE2E_VerifyByFile_UnsupportedMediaType(t *testing.T) {
	env := setupE2E(t)
	defer env.cleanup()

	status, _ := postVerifyFile(t, env, "doc.pdf", "application/pdf", []byte("%PDF-1.4"))
	if status != http.StatusUnsupportedMediaType {
		t.Fatalf("status = %d, want 415", status)
	}
}

// --- Multi-certificate selection --------------------------------------------

func TestE2E_MultipleCerts_VerifySelectsCorrectOne(t *testing.T) {
	env := setupE2E(t)
	defer env.cleanup()

	aletheia := loadTestdata(t, "aletheia.jpg")
	other := patternedJPEG(t, 512, 512)

	if status, _ := postCertify(t, env, "aletheia.jpg", "image/jpeg", aletheia, "alice"); status != http.StatusCreated {
		t.Fatalf("certify aletheia status = %d", status)
	}
	if status, _ := postCertify(t, env, "other.jpg", "image/jpeg", other, "bob"); status != http.StatusCreated {
		t.Fatalf("certify other status = %d", status)
	}

	if n := countCertificates(t, env.db); n != 2 {
		t.Fatalf("expected 2 certificates seeded, got %d", n)
	}

	wantHash := sha256Hex(aletheia)

	rotated := loadTestdata(t, "aletheia-rotated-90.jpg")
	status, respBody := postVerifyFile(t, env, "rotated.jpg", "image/jpeg", rotated)
	if status != http.StatusOK {
		t.Fatalf("status = %d body=%s, want 200", status, respBody)
	}
	v := decodeVerify(t, respBody)
	if !v.Certified || v.Certificate == nil {
		t.Fatalf("expected certified, got %+v", v)
	}
	if v.Certificate.ContentHash != wantHash {
		t.Errorf("matched the wrong certificate: got hash=%s want %s",
			v.Certificate.ContentHash, wantHash)
	}
	if v.Certificate.Registrant != "alice" {
		t.Errorf("registrant = %q, want alice", v.Certificate.Registrant)
	}
}

func TestE2E_MultipleCerts_FilterRejectedDoesNotMatchOther(t *testing.T) {
	env := setupE2E(t)
	defer env.cleanup()

	aletheia := loadTestdata(t, "aletheia.jpg")
	other := patternedJPEG(t, 512, 512)

	if status, _ := postCertify(t, env, "aletheia.jpg", "image/jpeg", aletheia, "alice"); status != http.StatusCreated {
		t.Fatalf("certify aletheia status = %d", status)
	}
	if status, _ := postCertify(t, env, "other.jpg", "image/jpeg", other, "bob"); status != http.StatusCreated {
		t.Fatalf("certify other status = %d", status)
	}

	filtered := loadTestdata(t, "aletheia-filter-2.jpg")
	status, respBody := postVerifyFile(t, env, "filter.jpg", "image/jpeg", filtered)
	if status != http.StatusNotFound {
		t.Fatalf("status = %d body=%s, want 404", status, respBody)
	}
	if v := decodeVerify(t, respBody); v.Certified {
		t.Errorf("filtered image should not match any certificate: %+v", v)
	}
}

// --- helpers for synthetic images -------------------------------------------

func solidColorJPEG(t *testing.T, w, h int, c color.Color) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 85}); err != nil {
		t.Fatalf("encode: %v", err)
	}
	return buf.Bytes()
}

// patternedJPEG creates a deterministic high-texture image distinct from
// aletheia, used as a "definitely unrelated" reference in multi-cert tests.
func patternedJPEG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			r := uint8((x*7 + y*3) % 256)
			g := uint8((x*5 ^ y*11) % 256)
			b := uint8((x*y/3 + y*y/7) % 256)
			img.Set(x, y, color.RGBA{R: r, G: g, B: b, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 85}); err != nil {
		t.Fatalf("encode: %v", err)
	}
	return buf.Bytes()
}
