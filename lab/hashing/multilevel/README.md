# Parallel Multilevel Hashing Lab

This lab implements a distributed 3-layer verification pipeline:

1.  **L1 (SHA-256):** Identical binary files.
2.  **L2 (pHash):** Structural similarity (resizing, recompression). Tolerate 1-bit difference.
3.  **L3 (MobileViT + LSH):** Semantic similarity using parallel tiling. Tolerate 5% difference (95%+ match).

## Architecture

- **Hasher (Client):** Orchestrates the pipeline, tiles images, and sends tiles in parallel to workers.
- **ONNX Workers (Server):** 5 distributed containers running the MobileViT model via ONNX Runtime.

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

- **Parallel Tiling:** The client processes all 224x224 tiles of an image concurrently using goroutines.
- **Distributed Inference:** Requests are distributed across 5 worker nodes to maximize CPU utilization.
- **Binary Protocol:** Data is exchanged between client and server using raw `float32` binary streams for minimum overhead.
