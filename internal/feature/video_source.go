package feature

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"gocv.io/x/gocv"

	"github.com/waizbart/aletheia-api/internal/domain"
	"github.com/waizbart/aletheia-api/internal/usecase"
)

const (
	// videoTempPattern names the spill file. OpenCV has no IMDecode equivalent
	// for video — VideoCaptureFile takes a path — so an uploaded video has to
	// exist on disk for as long as it is being decoded.
	//
	// The pattern is fixed and never derived from the uploaded filename.
	// FFmpeg's avformat_open_input resolves "proto:" prefixes inside a path, so
	// a caller-controlled name is a route to a protocol handler rather than a
	// local file.
	videoTempPattern = "aletheia-vid-*.bin"
	videoTempPrefix  = "aletheia-vid-"

	// staleTempAge is how old an orphaned spill file must be before startup
	// removes it. Close deletes the file on every ordinary path, but it cannot
	// run after SIGKILL or after OpenCV takes the process down with a SIGSEGV —
	// a risk this service already carries, which is why minFeatureDimension
	// exists.
	staleTempAge = time.Hour

	// propOrientationAuto is CAP_PROP_ORIENTATION_AUTO. gocv v0.31.0 stops
	// naming properties at VideoCaptureBitrate (47), so this is a raw int;
	// CAP_PROP_ORIENTATION_META is 48 should diagnostics ever want it.
	//
	// Auto-rotation is pinned rather than inherited from whatever the local
	// OpenCV build defaults to: a tool that strips an MP4 display matrix
	// without baking the rotation into pixels would otherwise yield a sideways
	// frame set on one machine and an upright one on another, and the frame
	// hashes are committed on chain.
	propOrientationAuto = gocv.VideoCaptureProperties(49)
)

// VideoLimits are the ingestion bounds the adapter enforces itself.
//
// MaxBytes has to live here rather than in the use case because it applies
// while the body is still streaming: once four gigabytes have landed on disk
// there is nothing left to refuse. The duration and resolution ceilings are
// policy and belong to domain.VideoProbe.Validate, which the use case calls.
type VideoLimits struct {
	MaxBytes    int64
	TempDir     string
	Concurrency int
}

// VideoExtractor decodes whole videos into the reduction a certificate stores.
//
// Unlike the other adapters in this package it imports the use case layer,
// because the port it satisfies hands back an interface rather than a shared
// domain type. Dependencies still point inward: feature depends on usecase,
// never the reverse.
type VideoExtractor struct {
	limits VideoLimits
	sem    chan struct{}
}

// NewVideoExtractor builds the decoder and sweeps spill files left by a
// previous process.
func NewVideoExtractor(limits VideoLimits) *VideoExtractor {
	if limits.MaxBytes <= 0 {
		limits.MaxBytes = 256 << 20
	}
	if limits.Concurrency <= 0 {
		// MAX_CONCURRENT_REQUESTS bounds requests, not decodes. Half the
		// available cores leaves room for everything else the process does
		// while a decode is running.
		limits.Concurrency = max(1, runtime.GOMAXPROCS(0)/2)
	}
	if limits.TempDir == "" {
		limits.TempDir = os.TempDir()
	}

	e := &VideoExtractor{limits: limits, sem: make(chan struct{}, limits.Concurrency)}
	e.sweepStaleTempFiles(time.Now())
	return e
}

// Close exists for symmetry with OpenCVExtractor. The decoder holds no
// process-wide resources of its own.
func (e *VideoExtractor) Close() {}

// sweepStaleTempFiles removes spill files this process did not create.
func (e *VideoExtractor) sweepStaleTempFiles(now time.Time) {
	entries, err := os.ReadDir(e.limits.TempDir)
	if err != nil {
		log.Printf("video extractor: cannot sweep %s: %v", e.limits.TempDir, err)
		return
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), videoTempPrefix) {
			continue
		}
		info, ierr := entry.Info()
		if ierr != nil || now.Sub(info.ModTime()) < staleTempAge {
			continue
		}
		path := filepath.Join(e.limits.TempDir, entry.Name())
		if rerr := os.Remove(path); rerr != nil {
			log.Printf("video extractor: cannot remove stale spill %s: %v", path, rerr)
		}
	}
}

// acquire takes a decode slot, honouring cancellation so an abandoned request
// does not hold one.
func (e *VideoExtractor) acquire(ctx context.Context) (func(), error) {
	select {
	case e.sem <- struct{}{}:
	case <-ctx.Done():
		return nil, fmt.Errorf("video ingest: waiting for a decode slot: %w", ctx.Err())
	}

	var once sync.Once
	return func() { once.Do(func() { <-e.sem }) }, nil
}

// videoSource is an ingested video: a spill file, the hash of the bytes that
// produced it, and what the container header claims.
type videoSource struct {
	path        string
	contentHash string
	probe       domain.VideoProbe

	release  func()
	closeMu  sync.Mutex
	isClosed bool
}

func (s *videoSource) ContentHash() string      { return s.contentHash }
func (s *videoSource) Probe() domain.VideoProbe { return s.probe }

// Close removes the spill file and frees the decode slot. It is idempotent:
// the use cases close a handle on every path, including the ones that also
// return an error, so a double close must not double-release the semaphore.
func (s *videoSource) Close() error {
	s.closeMu.Lock()
	defer s.closeMu.Unlock()
	if s.isClosed {
		return nil
	}
	s.isClosed = true

	if s.release != nil {
		s.release()
	}
	if s.path == "" {
		return nil
	}
	if err := os.Remove(s.path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("video source: removing %s: %w", s.path, err)
	}
	return nil
}

// Ingest streams the upload to a spill file, hashing as it goes, and reads the
// container header.
//
// The hash is computed during the copy rather than by a second read: the
// duplicate check needs it before the decode is paid for, and a video is far
// too large to hold in memory for a second pass.
func (e *VideoExtractor) Ingest(ctx context.Context, r io.Reader) (usecase.VideoHandle, error) {
	if r == nil {
		return nil, fmt.Errorf("video ingest: nil reader")
	}

	release, err := e.acquire(ctx)
	if err != nil {
		return nil, err
	}

	src := &videoSource{release: release}
	settled := false
	defer func() {
		if !settled {
			src.Close()
		}
	}()

	f, err := os.CreateTemp(e.limits.TempDir, videoTempPattern)
	if err != nil {
		return nil, fmt.Errorf("video ingest: creating spill file: %w", err)
	}
	src.path = f.Name()

	digest := sha256.New()
	// Reading one byte past the ceiling is what makes the limit detectable:
	// a copy that stops exactly at the limit cannot tell a file that fits from
	// one that was truncated.
	written, copyErr := io.Copy(io.MultiWriter(f, digest), io.LimitReader(r, e.limits.MaxBytes+1))
	if copyErr != nil {
		f.Close()
		return nil, fmt.Errorf("video ingest: buffering upload: %w", copyErr)
	}
	if written > e.limits.MaxBytes {
		f.Close()
		return nil, fmt.Errorf("video ingest: upload is over %d bytes: %w",
			e.limits.MaxBytes, domain.ErrVideoTooLarge)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return nil, fmt.Errorf("video ingest: flushing spill file: %w", err)
	}
	if err := f.Close(); err != nil {
		return nil, fmt.Errorf("video ingest: closing spill file: %w", err)
	}

	src.contentHash = hex.EncodeToString(digest.Sum(nil))

	probe, err := probeVideo(src.path)
	if err != nil {
		return nil, err
	}
	probe.SizeBytes = written
	src.probe = probe

	settled = true
	return src, nil
}

// probeVideo reads the container header without decoding a frame.
func probeVideo(path string) (domain.VideoProbe, error) {
	capture, err := openCapture(path)
	if err != nil {
		return domain.VideoProbe{}, err
	}
	defer capture.Close()

	fps := capture.Get(gocv.VideoCaptureFPS)
	frames := int(capture.Get(gocv.VideoCaptureFrameCount))
	width := int(capture.Get(gocv.VideoCaptureFrameWidth))
	height := int(capture.Get(gocv.VideoCaptureFrameHeight))

	probe := domain.VideoProbe{Width: width, Height: height, FrameCount: frames, FPS: fps}
	if fps > 0 && frames > 0 {
		probe.DurationMs = int(float64(frames) / fps * 1000)
	}
	return probe, nil
}

// openCapture opens a container with the FFmpeg backend pinned.
//
// The backend is never left to auto-selection. libopencv-highgui is installed
// in the runtime image, so OpenCV may reach for GStreamer, whose seek and
// rotation semantics differ — and this design cannot tolerate certify and
// verify sampling a file differently.
func openCapture(path string) (*gocv.VideoCapture, error) {
	capture, err := gocv.VideoCaptureFileWithAPI(path, gocv.VideoCaptureFFmpeg)
	if err != nil {
		return nil, fmt.Errorf("video: opening container: %w (%v)", domain.ErrVideoUndecodable, err)
	}
	if !capture.IsOpened() {
		capture.Close()
		return nil, fmt.Errorf("video: container did not open: %w", domain.ErrVideoUndecodable)
	}
	capture.Set(propOrientationAuto, 1)
	return capture, nil
}
