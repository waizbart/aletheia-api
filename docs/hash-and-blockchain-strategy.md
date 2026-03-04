# Hashing & Blockchain Strategy for Aletheia API

This document proposes a practical path for two roadmap features:

1. More robust image hashing that survives byte-level changes such as recompression.
2. Implementing a production blockchain service and selecting the best network.

## Current state (baseline)

- The API currently computes a strict SHA-256 digest of file bytes.
- SHA-256 is excellent for exact binary equality but fails if an image is re-encoded, resized, or metadata changes.
- Blockchain registration is currently a stub implementation.

## Feature 1: Robust image hashing that tolerates compression/transcoding

## Recommendation: use a **dual-hash strategy**

Store and use two hash families per asset:

1. **Cryptographic hash (SHA-256)** for exact file integrity and legal-grade immutability of the original upload.
2. **Perceptual hash** for visual similarity and resilience to lossy transforms.

### Why this is the best fit

- SHA-256 alone has false negatives for "same visual content, different bytes".
- Perceptual hash alone is not collision-resistant enough for ownership/dispute claims.
- Combined, you get both:
  - strict evidence (`sha256`)
  - robust matching (`phash`/`dhash`/`whash`)

## Practical algorithm choices

For v1 in production:

- Keep `sha256` as-is.
- Add **pHash (64-bit)** as primary perceptual hash.
- Optionally add **dHash (64-bit)** as a second perceptual signal if you want better robustness against specific transforms.

Matching policy suggestion:

- `distance == 0` -> exact perceptual match
- `distance <= 8` -> likely same content (high confidence)
- `distance 9-15` -> possible match (manual or secondary checks)
- `distance > 15` -> likely different

(Thresholds should be tuned using your own dataset.)

## Data model evolution

Extend certificate storage with fields similar to:

- `sha256` (existing `content_hash`)
- `phash` (uint64 stored as hex string or bigint)
- `dhash` (optional)
- `hash_version` (e.g., `v1`)
- `image_width`, `image_height` (optional metadata)

Keep verification logic as:

1. Check exact `sha256` first (fast/strict).
2. If no hit and input is image, compute perceptual hash and query nearest neighbors by Hamming distance.
3. Return result with confidence + distance details.

## Implementation notes in Go

- Use a mature Go image hashing library (e.g., `goimagehash`) for `pHash`/`dHash`.
- Normalize image decode pipeline:
  - strip metadata influence
  - convert to deterministic grayscale for pHash
  - guard against huge uploads (decode limits)
- Store perceptual hashes in a type that supports indexed similarity search.

## Risks and mitigations

- **Adversarial near-collisions**: never use perceptual hash as sole legal proof.
- **False positives** on similar scenes: combine pHash + dHash + optional lightweight ML embedding later.
- **Performance** under large volume: precompute and index Hamming-space candidates.

## Feature 2: Blockchain network suggestion

## Recommendation by stage

### Development / QA

- **Polygon Amoy** or **Ethereum Sepolia**.
- Choose based on team familiarity and infrastructure access.

### Production (default recommendation)

- **Polygon PoS mainnet** if your priority is low fees + fast confirmation for high certificate volume.

### Production (alternative)

- **Ethereum mainnet** if your top priority is maximum ecosystem trust and long-term immutability social consensus, and you can afford fees.

## Why Polygon PoS is likely best for this project

Your product appears certificate-volume oriented. On-chain cost per registration matters:

- Lower gas allows certifying many assets without pricing out users.
- EVM compatibility keeps integration simple with your current architecture.
- You can still anchor trust by periodic checkpointing/bridging strategies if desired.

## Contract and transaction design recommendations

- Use a minimal contract that stores `(sha256, phash, timestamp, issuer)` event logs.
- Favor emitting events over heavy storage where possible to reduce gas.
- Implement idempotency (`alreadyRegistered(hash)`) to avoid duplicate writes.
- Include `hash_version` in emitted data for migration safety.

## Reliability and operations checklist

Before replacing the stub service:

1. Add retry/backoff and nonce management.
2. Distinguish `pending` vs `finalized` confirmations.
3. Persist transaction lifecycle states (`submitted`, `mined`, `confirmed`, `failed`).
4. Add reorg-safe confirmation depth.
5. Add observability (RPC latency, tx failure rates, gas usage).

## Suggested phased rollout

### Phase 1 (fastest path)

- Keep existing flow.
- Add perceptual hashing on upload/verify.
- Keep on-chain payload to `sha256` only.

### Phase 2

- Store on-chain both `sha256` and `phash` event fields.
- Expose verify API response with exact vs perceptual match signals.

### Phase 3

- Add optional embedding-based similarity search for difficult cases.
- Add periodic anchoring to a higher-trust chain if needed.

## Final decision summary

- **Hashing:** adopt `SHA-256 + pHash` (optionally `+ dHash`) rather than replacing SHA-256.
- **Network:** use **Polygon PoS** for production cost/performance, keep **Sepolia/Amoy** for test.
- **Architecture:** keep blockchain adapter interface, replace stub with an EVM adapter plus robust tx state handling.
