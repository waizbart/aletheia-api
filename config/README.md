# Attestation roots

`ANDROID_ATTESTATION_ROOTS` must point at a PEM bundle of Google's hardware
attestation root certificates. They are published by Google and are what makes
the attestation check meaningful: without a pinned root, a self-signed chain
verifies and the check proves nothing.

Fetch the current bundle from Google's hardware attestation documentation and
save it here as `android-attestation-roots.pem`. The file is deliberately not
committed — pinning a copy that silently goes stale is worse than fetching it
deliberately, and the roots do rotate.

With no roots configured the API still starts and still serves verification;
device enrolment reports every platform as unsupported until you provide them.
