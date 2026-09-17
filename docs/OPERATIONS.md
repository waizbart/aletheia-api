# Operations

Running Aletheia in production: what to configure, how to onboard a customer,
how anchoring behaves, and what to do when something breaks.

## Access model

| Level | Credential | Reaches |
|---|---|---|
| Public | none | `/health`, `/docs`, verification |
| Tenant | `Authorization: Bearer alk_…` | captures, device enrolment, usage |
| Admin | `Authorization: Bearer $ADMIN_API_TOKEN` | org and key management, certificate deletion, dashboard |

`ADMIN_API_TOKEN` **fails closed**. Leaving it unset does not disable the guard;
it makes every admin route reject every request. The opposite default is how
registries get wiped.

Generate one with `openssl rand -hex 32` and keep it out of the repository.

## Onboarding a customer

Deliberately not self-serve. A registrant is only worth something if somebody
vetted them, and that vetting is the moat.

```bash
ADMIN="Authorization: Bearer $ADMIN_API_TOKEN"

# 1. Create the organisation
curl -sX POST https://api.example.com/admin/orgs \
  -H "$ADMIN" -H 'Content-Type: application/json' \
  -d '{"name":"Acme Seguros","plan":"growth"}'

# 2. Issue a key. The plaintext is returned exactly once — it is not stored
#    and cannot be recovered, only replaced.
curl -sX POST https://api.example.com/admin/orgs/<org-id>/keys \
  -H "$ADMIN" -H 'Content-Type: application/json' \
  -d '{"name":"production"}'

# 3. Revoke when needed
curl -sX DELETE https://api.example.com/admin/keys/<key-id> -H "$ADMIN"
```

Plans and their monthly allowances live in `internal/domain/org.go`. An
unrecognised plan falls back to the developer allowance, never to unlimited: a
configuration typo must not become free service.

## Metering

Attested captures are checked against the allowance *before* the work starts
and counted *after* it succeeds, so a rejected capture never reaches an
invoice. A lost usage count never fails an operation that already succeeded —
an under-count is a billing problem, a failed capture is a customer problem.

Video is metered as `attested_video_capture`, separate from
`attested_capture`, because decoding a whole file costs orders of magnitude
more than decoding one image. The developer plan allows 50 a month against 500
image captures; growth and enterprise are uncapped for both.

On the video path the upload is parsed *before* the allowance is checked, which
is the reverse of everywhere else. The media kind lives in the multipart part
header, so there is no way to know which allowance applies until the part has
been read. The size ceilings still bound what an over-quota tenant can make the
server buffer.

Anonymous verification is free and never counted. Presenting a key on the same
route opts into metered use, which is how the free tier stays impossible to
break by accident. Video verification is additionally size-tiered: an
unauthenticated caller gets 32 MB and an authenticated one 256 MB, because a
256 MB decode per anonymous request is a denial of service offered to the
internet. Verifying by hash stays free and unrestricted, and it is the cheap
path.

Counters live in `usage_counters`, keyed by `(org, operation, UTC month)` and
incremented with an upsert so concurrent captures cannot lose a count.

`GET /usage` returns a customer's own current-period consumption. A `null`
limit means the plan does not cap that operation.

## Anchoring

A background worker collects unanchored certificates, commits them under one
Merkle root, and attaches each certificate's inclusion proof.

```
ANCHOR_INTERVAL     how often a batch is committed (default 1h)
ANCHOR_BATCH_SIZE   maximum certificates per batch (default 4096)
ANCHOR_GAS_LIMIT    default 120000
ANCHOR_PRIVATE_KEY  the account that signs anchors
CHAIN_ID            137 for Polygon, 31337 for a local Anvil
```

The anchoring address is logged at startup — **fund it**, or every pass fails
and the backlog grows.

A longer interval is strictly cheaper: the cost of an anchor does not change
with the number of certificates under it. The tradeoff is latency to first
proof, and proof size, which grows with log2(batch size).

**Failure behaviour.** If the transaction does not confirm, the batch stays
pending and the next pass retries it. Re-anchoring costs one extra transaction;
marking certificates anchored against a root that never landed would hand out
proofs of nothing. That asymmetry is why the worker errs the way it does.

If the transaction was *broadcast* before the receipt wait failed, its hash is
written to `anchors` with status `pending` and no certificates attached, and
logged. That row is a reconciliation handle, not a claim: nothing points at it,
so no certificate advertises a proof against it. It may still be mined, in
which case the chain carries a root the retry has already superseded — check
these rows before assuming a root on chain is unaccounted for.

```sql
SELECT id, tx_hash, leaf_count, created_at
  FROM anchors WHERE status = 'pending' ORDER BY created_at DESC;
```

**Verifying a proof independently.** A certificate's `anchor` block carries
`tx_hash`, `leaf_index` and `merkle_proof`. Recompute the leaf as
`keccak256(keccak256(contentHash ‖ featureCommitment))`, then call
`AnchorRegistry.verify(root, leaf, proof)`. No trust in this API is required.

## Deployment checklist

- [ ] `ADMIN_API_TOKEN` set to a freshly generated secret
- [ ] TLS terminated in front of the API (Caddy is the least-ops option)
- [ ] `CORS_ALLOWED_ORIGINS` restricted to real origins, not `*`
- [ ] `TRUSTED_PROXY_HOPS` set to the number of proxies you actually operate —
      zero if the process is exposed directly. The client address is read that
      many entries from the *right* of `X-Forwarded-For`, because proxies
      append and the leftmost entry is whatever the caller sent. Understating
      it lets a caller forge a fresh identity per request and walk past the
      rate limiter
- [ ] `ANDROID_ATTESTATION_ROOTS` populated (see `config/README.md`) — with
      all three `ANDROID_*` variables unset the API still starts and verifies,
      but enrolment answers 501
- [ ] `ANDROID_ALLOWED_PACKAGES` and `ANDROID_SIGNATURE_DIGESTS` set to your app
- [ ] `ALLOW_UNATTESTED_CERTIFY` left at `false`
- [ ] Anchoring account funded
- [ ] `pg_dump` on a schedule, with a **tested** restore
- [ ] Secrets supplied by the deployment environment, not a committed `.env`

## Abuse controls

```
RATE_LIMIT_RPS            sustained requests per second per client IP (default 20)
RATE_LIMIT_BURST          burst allowance on top (default 40)
MAX_CONCURRENT_REQUESTS   in-flight cap (default 32)
VIDEO_MAX_UPLOAD_BYTES    byte ceiling for a video upload (default 256 MB)
VIDEO_MAX_DURATION_MS     duration ceiling, a compile-time 2 min today
VIDEO_MAX_CONCURRENCY     concurrent decodes (default: half the cores)
VIDEO_TEMP_DIR            where the decoder's spill files go
```

The concurrency cap is the practical bound on peak memory: upload routes decode
whole images, so a handful of concurrent 100 MB uploads is otherwise enough to
exhaust the process. Requests over the cap fail fast with 503 rather than
queueing behind a full buffer.

### Video decoding

`MAX_CONCURRENT_REQUESTS` bounds requests, not decodes, so video has its own
semaphore inside the extractor. It is acquired before the upload is spilled
rather than before the decode, because otherwise every concurrent request
writes its full payload to disk before any bound applies.

**Temp disk.** OpenCV has no `IMDecode` equivalent for video —
`VideoCaptureFile` takes a path — so an uploaded video exists on disk while it
is being decoded and is removed when the request ends. Budget
`VIDEO_MAX_CONCURRENCY × VIDEO_MAX_UPLOAD_BYTES` and then **double it**: Go's
multipart parser already spills parts over 32 MB to `os.TempDir()` before this
process sees them. The compose file mounts a sized `tmpfs` for this.

Spill files are named from a fixed pattern and never from the uploaded
filename, because FFmpeg's `avformat_open_input` resolves `proto:` prefixes
inside a path. Startup removes spill files older than an hour: `Close` deletes
them on every ordinary path but cannot run after a `SIGKILL` or after OpenCV
takes the process down.

**Decoder exposure.** `libavcodec` is a large C surface being fed untrusted
bytes. The duration and resolution ceilings reduce exposure but do not remove
it — reading the container header already instantiates a decoder context — so
**no in-process mitigation here is complete**. Run the API under an automatic
restart policy with a memory cgroup limit; a seccomp profile is worth the
effort; moving the decode into a subprocess is the real fix and is not done.
This service already lives with this class of risk: `minFeatureDimension`
exists because OpenCV reads out of bounds on a tiny image and takes the whole
process down with it.

**Slow uploaders.** `ReadTimeout` and `WriteTimeout` are deliberately unset on
the server, for 100 MB uploads and for the dashboard's SSE stream. The video
path sets a per-request read deadline instead, so a slow client cannot hold a
decode slot open indefinitely without disturbing either.

## Runbook

**Anchoring stopped.** Check the worker log line at startup for the anchoring
address, then check its balance. Next: RPC reachability, and whether
`eth_gasPrice` is returning something sane. Certificates keep being issued
while anchoring is down — they simply stay pending and land in a later batch.

**A device is compromised.** `POST /devices/{id}/revoke`. New captures stop
immediately; existing certificates are untouched, which is deliberate — they
are the record of what the device did. Then decide, as a policy question,
whether certificates from that device need to be re-examined.

Revocation follows the attested key, not the row: the key is unique across the
registry, and re-enrolling a revoked one is refused. Otherwise the device could
simply enrol again and receive a fresh active record.

**An API key leaked.** `DELETE /admin/keys/{id}`, then issue a replacement. Keys
are independent, so revoking one does not disturb the customer's other
integrations.

**Enrolment suddenly failing for everyone.** Most likely the attestation roots
rotated, or the app was re-signed and `ANDROID_SIGNATURE_DIGESTS` is stale. The
rejection reason returned to the client names the failed gate.

**Certification slow.** Certification no longer touches the chain, so suspect
OpenCV feature extraction or Postgres. The `/observability` dashboard shows
per-stage latency for every request.

**Verification slow.** Negative queries are the expensive case: they pay an ORB
re-check against up to `verifyTopK` candidates. Watch the LSH candidate count in
the dashboard — if it is climbing with corpus size, the prefilter is saturating
and the band configuration needs revisiting.
