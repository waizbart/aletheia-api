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

Anonymous verification is free and never counted. Presenting a key on the same
route opts into metered use, which is how the free tier stays impossible to
break by accident.

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
```

The concurrency cap is the practical bound on peak memory: upload routes decode
whole images, so a handful of concurrent 100 MB uploads is otherwise enough to
exhaust the process. Requests over the cap fail fast with 503 rather than
queueing behind a full buffer.

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
