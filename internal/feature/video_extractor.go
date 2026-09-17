package feature

import (
	"context"
	"fmt"
	"sort"

	"gocv.io/x/gocv"

	"github.com/waizbart/aletheia-api/internal/domain"
	"github.com/waizbart/aletheia-api/internal/usecase"
)

const (
	// anchorCandidates is how many of the sharpest sampled frames are kept for
	// anchor selection. Sharpness rejects black frames, flat frames and motion
	// blur, but it cannot tell whether ORB will find enough structure, so the
	// runners-up are kept as fallbacks. Three Mats of held memory buys three
	// attempts; more would rarely be reached.
	anchorCandidates = 3

	// maxDecodeFrames bounds the counting pass for containers whose header
	// reports no frame count. Such a container also reports no duration, so
	// VideoProbe's duration ceiling cannot bound it and this budget is the only
	// thing that does. It is the duration ceiling at 60fps.
	maxDecodeFrames = domain.MaxVideoDurationMs / 1000 * 60
)

// Reduce decodes the sampled frames and reduces the video to a frame sequence
// plus one anchor frame.
//
// Decoding is a single forward pass: Grab skips a frame's colour conversion and
// Mat copy but still decodes it, so the sampled frames cost K conversions
// rather than N. Seeking with CAP_PROP_POS_FRAMES is deliberately not used —
// FFmpeg seeks to the preceding keyframe and decodes forward, which lands
// correctly on a well-indexed MP4 and silently wrongly on a fragmented or
// variable-frame-rate one. Certify and verify disagreeing about which frames
// they sampled is the one failure this design cannot absorb.
func (e *VideoExtractor) Reduce(ctx context.Context, h usecase.VideoHandle) (*usecase.VideoReduction, error) {
	src, ok := h.(*videoSource)
	if !ok || src == nil || src.path == "" {
		return nil, fmt.Errorf("video reduce: handle did not come from this extractor")
	}

	frameCount, err := resolveFrameCount(src)
	if err != nil {
		return nil, err
	}

	capture, err := openCapture(src.path)
	if err != nil {
		return nil, err
	}
	defer capture.Close()

	keeper := &anchorKeeper{limit: anchorCandidates}
	defer keeper.Close()

	hashes := make([][32]byte, 0, domain.VideoFrameSamples)
	cursor := -1
	var previous [32]byte

	for _, target := range domain.SampleFrameIndexes(frameCount) {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("video reduce: %w", err)
		}

		// A video with fewer frames than slots maps several slots onto the same
		// frame. Repeating its hash is correct and saves a decode.
		if target == cursor {
			hashes = append(hashes, previous)
			continue
		}

		if skip := target - cursor - 1; skip > 0 {
			capture.Grab(skip)
		}

		frame := gocv.NewMat()
		if !capture.Read(&frame) || frame.Empty() {
			// The header over-reported the frame count, which is common on
			// variable frame rate. Truncating is honest; padding would invent
			// frames that were never decoded.
			frame.Close()
			break
		}
		cursor = target

		resized := resizeBGR(frame, domain.ResizeMax)
		frame.Close()

		img, ierr := resized.ToImage()
		if ierr != nil {
			resized.Close()
			return nil, fmt.Errorf("video reduce: converting frame %d: %w", target, ierr)
		}

		previous = domain.PHash256FromImage(img)
		hashes = append(hashes, previous)

		// The keeper takes ownership of resized and closes whatever it drops,
		// so no frame Mat outlives the loop except the retained candidates.
		keeper.offer(len(hashes)-1, sharpness(resized), resized)
	}

	if len(hashes) == 0 {
		return nil, fmt.Errorf("video reduce: no frame decoded: %w", domain.ErrVideoUndecodable)
	}

	anchorSlot, anchorSig, anchorPNG, err := selectAnchor(keeper)
	if err != nil {
		return nil, err
	}

	return &usecase.VideoReduction{
		Signature: &domain.VideoSignature{
			FramePHashes: hashes,
			AnchorIndex:  anchorSlot,
			DurationMs:   src.probe.DurationMs,
		},
		Anchor:      anchorSig,
		AnchorPHash: hashes[anchorSlot],
		AnchorFrame: anchorPNG,
	}, nil
}

// selectAnchor walks the retained candidates from sharpest down, taking the
// first that yields an ORB signature.
//
// Sharpness is a proxy: a crisp frame of a blank wall scores well and still has
// nothing for ORB to latch onto. Trying the runners-up costs three extractions
// in the worst case, against the one-to-two seconds that running ORB on all 32
// frames just to rank them would cost.
func selectAnchor(k *anchorKeeper) (int, *domain.FeatureSignature, []byte, error) {
	var lastErr error
	for _, cand := range k.ranked() {
		sig, err := signatureFromResizedBGR(cand.mat)
		if err != nil {
			lastErr = err
			continue
		}
		// PNG, not JPEG. The anchor frame is what verify feeds to Match, whose
		// colour residual would otherwise be measuring a second lossy pass
		// rather than the difference between two videos.
		png, perr := encodeFramePNG(cand.mat)
		if perr != nil {
			lastErr = perr
			continue
		}
		return cand.slot, sig, png, nil
	}

	if lastErr == nil {
		lastErr = domain.ErrVideoUndecodable
	}
	return 0, nil, nil, fmt.Errorf("video reduce: no usable anchor frame: %w", lastErr)
}

// FrameAt re-decodes the video and returns one sampled frame as PNG.
//
// It costs a second forward pass, which is why verify only reaches for it after
// the cheap gates — the frame sequence and the duration — have already accepted
// the candidate. In practice that is zero or one candidate per request.
func (e *VideoExtractor) FrameAt(ctx context.Context, h usecase.VideoHandle, slot int) ([]byte, error) {
	src, ok := h.(*videoSource)
	if !ok || src == nil || src.path == "" {
		return nil, fmt.Errorf("video frame: handle did not come from this extractor")
	}

	frameCount, err := resolveFrameCount(src)
	if err != nil {
		return nil, err
	}

	indexes := domain.SampleFrameIndexes(frameCount)
	if slot < 0 || slot >= len(indexes) {
		return nil, fmt.Errorf("video frame: slot %d is outside the %d sampled slots", slot, len(indexes))
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("video frame: %w", err)
	}

	capture, err := openCapture(src.path)
	if err != nil {
		return nil, err
	}
	defer capture.Close()

	if target := indexes[slot]; target > 0 {
		capture.Grab(target)
	}

	frame := gocv.NewMat()
	defer frame.Close()
	if !capture.Read(&frame) || frame.Empty() {
		return nil, fmt.Errorf("video frame: slot %d did not decode: %w", slot, domain.ErrVideoUndecodable)
	}

	resized := resizeBGR(frame, domain.ResizeMax)
	defer resized.Close()

	return encodeFramePNG(resized)
}

// resolveFrameCount trusts the container header when it says anything useful
// and counts frames when it does not.
func resolveFrameCount(src *videoSource) (int, error) {
	if src.probe.FrameCount > 0 {
		return src.probe.FrameCount, nil
	}

	// A fragmented or streamed container reports nothing useful in its header.
	// Counting costs a full decode pass, which is why it only happens when the
	// header left no alternative.
	counted, err := measureFrameCount(src.path)
	if err != nil {
		return 0, err
	}
	if counted <= 0 {
		return 0, fmt.Errorf("video: container has no frames: %w", domain.ErrVideoUndecodable)
	}
	return counted, nil
}

// measureFrameCount counts decodable frames, bounded by maxDecodeFrames.
func measureFrameCount(path string) (int, error) {
	capture, err := openCapture(path)
	if err != nil {
		return 0, err
	}
	defer capture.Close()

	frame := gocv.NewMat()
	defer frame.Close()

	n := 0
	for capture.Read(&frame) {
		if frame.Empty() {
			break
		}
		n++
		if n > maxDecodeFrames {
			return 0, fmt.Errorf("video reduce: over %d frames with no header duration: %w",
				maxDecodeFrames, domain.ErrVideoTooLong)
		}
	}
	return n, nil
}

// sharpness is the variance of the Laplacian: one number that rejects a black
// frame, a flat frame and a motion-blurred frame alike.
func sharpness(bgr gocv.Mat) float64 {
	gray := gocv.NewMat()
	defer gray.Close()
	gocv.CvtColor(bgr, &gray, gocv.ColorBGRToGray)

	lap := gocv.NewMat()
	defer lap.Close()
	gocv.Laplacian(gray, &lap, gocv.MatTypeCV64F, 1, 1, 0, gocv.BorderDefault)

	mean := gocv.NewMat()
	defer mean.Close()
	stddev := gocv.NewMat()
	defer stddev.Close()
	gocv.MeanStdDev(lap, &mean, &stddev)

	sd := stddev.GetDoubleAt(0, 0)
	return sd * sd
}

func encodeFramePNG(bgr gocv.Mat) ([]byte, error) {
	buf, err := gocv.IMEncode(gocv.PNGFileExt, bgr)
	if err != nil {
		return nil, fmt.Errorf("video reduce: encoding anchor frame: %w", err)
	}
	defer buf.Close()

	out := make([]byte, len(buf.GetBytes()))
	copy(out, buf.GetBytes())
	return out, nil
}

// anchorCandidate is a retained frame in the running for anchor.
type anchorCandidate struct {
	slot      int
	sharpness float64
	mat       gocv.Mat
}

// anchorKeeper retains the sharpest few frames and closes the rest as it goes.
//
// Holding candidates costs a handful of Mats; the alternative is a second
// decode pass over the whole file, which costs as much as the first. Note the
// deliberate absence of defer inside the sampling loop: a deferred Close would
// accumulate until the function returned, which at 32 frames is hundreds of
// megabytes of Mats per request.
type anchorKeeper struct {
	limit int
	items []anchorCandidate
}

// offer hands a frame to the keeper, which takes ownership of the Mat.
//
// Ranking is by descending sharpness with ties going to the earlier slot, so
// the choice is a pure function of the decoded frames and certify and verify
// cannot disagree about it.
func (k *anchorKeeper) offer(slot int, sharp float64, mat gocv.Mat) {
	k.items = append(k.items, anchorCandidate{slot: slot, sharpness: sharp, mat: mat})
	sort.SliceStable(k.items, func(i, j int) bool {
		if k.items[i].sharpness != k.items[j].sharpness {
			return k.items[i].sharpness > k.items[j].sharpness
		}
		return k.items[i].slot < k.items[j].slot
	})
	for len(k.items) > k.limit {
		last := len(k.items) - 1
		k.items[last].mat.Close()
		k.items = k.items[:last]
	}
}

func (k *anchorKeeper) ranked() []anchorCandidate { return k.items }

func (k *anchorKeeper) Close() {
	for i := range k.items {
		k.items[i].mat.Close()
	}
	k.items = nil
}
