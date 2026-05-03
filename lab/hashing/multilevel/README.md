# Parallel Multilevel Hashing Lab

This lab implements a distributed 2-layer verification pipeline for judicial
image comparison. Each image goes through a normalization step, then 2
verification levels.

## Pipeline (in order)

For **both** the reference image and each test image:

1. **Proportional Downscale (3MP cap)** — Reduces large images to at most
   2048×1536 pixels while preserving aspect ratio. Images smaller than 3MP
   are kept at original resolution.
2. **L1 — SHA-256 (Normalized identical):** Hash of the RGBA pixel data
   after downscale. If two images produce the exact same pixels after
   normalization, they match here. Full color preserved.
3. **L2 — MobileViT + SimHash (Semantic similarity at 4 orientations):**
   The standardized image is rotated to 0°, 90°, 180°, and 270°. Each
   rotation is tiled into 224×224 patches, each tile is sent to a
   distributed ONNX worker for MobileViT inference, and LSH/SimHash (64-bit)
   is applied to the feature vectors. All 4 sets of hashes are stored on
   the blockchain (8 bytes per tile × 4 orientations).

   During comparison, the test image is also tried at all 4 orientations.
   The best matching orientation pair (across 4×4 = 16 combinations)
   determines the result.

## Orientation Handling

Instead of auto-detecting rotation with Hough Transform (which produced
false positives on non-document photos), the pipeline brute-forces all
4 cardinal orientations. Tile hashes at each orientation are stored on the
blockchain, and the comparison takes the best match across all combos.

## Image Standardization

The 3MP proportional cap is critical for the judicial use case: camera and
surveillance footage often comes at very high resolutions (e.g. 12–50 MP).
The cap preserves more detail while keeping processing tractable.

## Architecture

- **Hasher (Client):** Orchestrates the pipeline, normalizes orientation,
  resizes proportionally (3MP cap), tiles, and sends tiles in parallel to
  workers.
- **ONNX Workers (Server):** 5 distributed containers running the MobileViT
  model via ONNX Runtime.

## Prerequisites

- Docker
- Docker Compose

## How to Run

1.  Navigate to the lab directory:

    ```bash
    cd lab/hashing/multilevel
    ```

2.  Start the distributed environment:

    ```bash
    docker compose up --build
    ```

    This will:
    - Spin up 5 replicas of the ONNX worker.
    - Start the hasher client.
    - Load balance requests across workers using Docker's internal DNS.

## Performance Optimization

- **Orientation on Full Image:** Hough Transform runs on the original
  resolution for maximum accuracy in angle detection.
- **Proportional Resize:** Images are resized with aspect ratio preserved,
  capped at 3 megapixels (no hard-coded square). Uses OpenCV with area
  interpolation for anti-aliasing.
- **Parallel Tiling:** The client processes all 224×224 tiles of the
  standardized image concurrently using goroutines.
- **Distributed Inference:** Requests are distributed across 5 worker nodes
  to maximize CPU utilization.
- **Binary Protocol:** Data is exchanged between client and server using raw
  `float32` binary streams for minimum overhead.
