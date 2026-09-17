//go:build e2e

package e2e_test

import (
	"bytes"
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"gocv.io/x/gocv"

	"github.com/waizbart/aletheia-api/internal/domain"
	"github.com/waizbart/aletheia-api/internal/usecase"
)

// Clips are generated rather than committed, and the painter is a function of
// normalized time so a clip with 50 frames and one with 60 show the same scene
// at the same instants — which is what an fps transcode does.
func videoFrameBytes(width, height int, t float64, blocksX, blocksY, salt int) []byte {
	buf := make([]byte, width*height*3)

	sqSize := 0.18
	sqX := 0.05 + t*(1-sqSize-0.1)
	sqY := 0.30 + 0.25*t

	for y := 0; y < height; y++ {
		v := (float64(y) + 0.5) / float64(height)
		for x := 0; x < width; x++ {
			u := (float64(x) + 0.5) / float64(width)

			bx := int(u * float64(blocksX))
			by := int(v * float64(blocksY))
			seed := (bx * 73856093) ^ (by * 19349663) ^ salt
			b := byte(40 + (seed&0x7fffffff)%180)
			g := byte(40 + ((seed>>7)&0x7fffffff)%180)
			r := byte(40 + ((seed>>13)&0x7fffffff)%180)

			if salt == 0 && u >= sqX && u < sqX+sqSize && v >= sqY && v < sqY+sqSize {
				b, g, r = 250, 250, 250
			}

			off := (y*width + x) * 3
			buf[off+0], buf[off+1], buf[off+2] = b, g, r
		}
	}
	return buf
}

// writeClip renders a clip and returns its bytes. MJPEG is present in every
// libavcodec build; an unavailable encoder skips rather than fails, because
// VideoWriterFile reports that through IsOpened and not through an error.
func writeClip(t *testing.T, name string, width, height, frames int, fps float64, salt int) []byte {
	t.Helper()

	blocksX, blocksY := 16, 12
	if salt != 0 {
		blocksX, blocksY = 9, 7
	}

	path := filepath.Join(t.TempDir(), name+".avi")
	writer, err := gocv.VideoWriterFile(path, "MJPG", fps, width, height, true)
	if err != nil {
		t.Skipf("cannot open an MJPG writer: %v", err)
	}
	if !writer.IsOpened() {
		writer.Close()
		t.Skip("the MJPG encoder is not available in this OpenCV build")
	}

	for i := 0; i < frames; i++ {
		tt := (float64(i) + 0.5) / float64(frames)
		mat, merr := gocv.NewMatFromBytes(height, width, gocv.MatTypeCV8UC3,
			videoFrameBytes(width, height, tt, blocksX, blocksY, salt))
		if merr != nil {
			writer.Close()
			t.Fatalf("building frame %d: %v", i, merr)
		}
		if werr := writer.Write(mat); werr != nil {
			mat.Close()
			writer.Close()
			t.Fatalf("writing frame %d: %v", i, werr)
		}
		mat.Close()
	}
	writer.Close()

	raw, rerr := os.ReadFile(path)
	if rerr != nil {
		t.Fatalf("read generated clip: %v", rerr)
	}
	if len(raw) == 0 {
		t.Skip("the MJPG encoder produced no output")
	}
	return raw
}

// certifyVideoBytes drives the attested certification path's final step: the
// capture use case would have ingested and checked the signature before this.
func certifyVideoBytes(t *testing.T, env *e2eEnv, raw []byte) *domain.Certificate {
	t.Helper()

	ctx := context.Background()
	handle, err := env.videos.Ingest(ctx, bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	defer handle.Close()

	if err := handle.Probe().Validate(); err != nil {
		t.Fatalf("probe: %v", err)
	}

	out, err := env.certifyVideo.Execute(ctx, usecase.CertifyVideoInput{
		Handle:     handle,
		Registrant: "e2e",
		OrgID:      "",
	})
	if err != nil {
		t.Fatalf("certify video: %v", err)
	}
	return out.Certificate
}

func postVerifyVideo(t *testing.T, env *e2eEnv, raw []byte) (int, []byte) {
	t.Helper()
	return doRequest(t, newMultipartReq(t, http.MethodPost,
		env.server.URL+"/certificates/verify", "clip.avi", "video/x-msvideo", raw))
}

func TestE2E_Video_WholeFileRoundTrip(t *testing.T) {
	env := setupE2E(t)
	defer env.cleanup()

	reference := writeClip(t, "reference", 640, 480, 50, 25, 0)
	cert := certifyVideoBytes(t, env, reference)

	if cert.MediaKind != domain.MediaKindVideo {
		t.Fatalf("MediaKind = %q, want video", cert.MediaKind)
	}
	if cert.Video == nil || cert.Video.FrameCount() != domain.VideoFrameSamples {
		t.Fatalf("frame sequence not persisted: %+v", cert.Video)
	}
	assertRowExists(t, env.db, cert.ContentHash)

	t.Run("verify by hash", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodGet,
			env.server.URL+"/certificates/verify?hash="+cert.ContentHash, nil)
		if err != nil {
			t.Fatal(err)
		}
		status, body := doRequest(t, req)
		if status != http.StatusOK {
			t.Fatalf("status = %d, want 200: %s", status, body)
		}
		if v := decodeVerify(t, body); !v.Certified {
			t.Error("certified = false, want true")
		}
	})

	t.Run("verify the exact file by upload", func(t *testing.T) {
		status, body := postVerifyVideo(t, env, reference)
		if status != http.StatusOK {
			t.Fatalf("status = %d, want 200: %s", status, body)
		}
	})

	// The whole point of the feature: the same video, re-encoded, still matches.
	reencodes := []struct {
		name          string
		width, height int
		frames        int
		fps           float64
	}{
		{"rescaled to 320x240", 320, 240, 50, 25},
		{"frame rate converted to 30fps", 640, 480, 60, 30},
		{"rescaled and frame rate converted", 480, 360, 60, 30},
	}
	for _, rc := range reencodes {
		t.Run("verify a re-encode: "+rc.name, func(t *testing.T) {
			raw := writeClip(t, "variant", rc.width, rc.height, rc.frames, rc.fps, 0)
			if sha256Hex(raw) == sha256Hex(reference) {
				t.Fatal("the variant is byte-identical to the reference; it would pass on the exact path")
			}

			status, body := postVerifyVideo(t, env, raw)
			if status != http.StatusOK {
				t.Fatalf("status = %d, want 200 — a re-encode of a certified video was rejected: %s",
					status, body)
			}
			v := decodeVerify(t, body)
			if !v.Certified || v.Certificate == nil || v.Certificate.ContentHash != cert.ContentHash {
				t.Errorf("matched %+v, want the reference certificate", v.Certificate)
			}
		})
	}

	t.Run("an unrelated video does not match", func(t *testing.T) {
		other := writeClip(t, "unrelated", 640, 480, 50, 25, 0x5bf03635)

		status, body := postVerifyVideo(t, env, other)
		if status != http.StatusNotFound {
			t.Fatalf("status = %d, want 404 for an unrelated video: %s", status, body)
		}
	})

	// Media kinds must not answer for one another: a still is not the video it
	// came from, and the certificate's content hash would not reproduce.
	t.Run("an image upload does not match a video certificate", func(t *testing.T) {
		status, body := doRequest(t, newMultipartReq(t, http.MethodPost,
			env.server.URL+"/certificates/verify", "still.jpg", "image/jpeg",
			loadTestdata(t, "aletheia.jpg")))
		if status != http.StatusNotFound {
			t.Errorf("status = %d, want 404: %s", status, body)
		}
	})
}

func TestE2E_Video_DuplicateIsRefused(t *testing.T) {
	env := setupE2E(t)
	defer env.cleanup()

	raw := writeClip(t, "dup", 480, 360, 40, 20, 0)
	certifyVideoBytes(t, env, raw)

	ctx := context.Background()
	handle, err := env.videos.Ingest(ctx, bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("second ingest: %v", err)
	}
	defer handle.Close()

	_, err = env.certifyVideo.Execute(ctx, usecase.CertifyVideoInput{Handle: handle, Registrant: "e2e"})
	if err == nil {
		t.Fatal("expected the duplicate to be refused")
	}
}
