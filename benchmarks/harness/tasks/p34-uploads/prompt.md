Work in the current directory (Go library "via", a reactive web framework). Implement this roadmap item:

**Chunked resumable uploads (EXPERIMENTAL)**
- Chunked upload POSTs to `/_upload/{token}/{index}`, with resume support (client can query which chunk is next after a dropped connection).
- Upload tokens: cryptographically random, bound to the issuing session (a token from session A must be rejected when presented by session B), 1-hour TTL, with a sweep that removes expired partial assemblies.
- Upload progress exposed as a built-in `Signal[int]` (percent).
- Size caps enforced at both chunk time and assembly time (reject assembly on size mismatch).
- Client-supplied filenames sanitized before use.
- Ship as EXPERIMENTAL (mark in doc comments).

Definition of done — all six of these tests written by you and green, plus `go vet ./` clean and `go test -race ./` green for the root package:
TestUpload_resumesFromNextChunkAfterDrop, TestUpload_rejectsAssemblyOnSizeMismatch, TestUpload_rejectsTokenFromOtherSession, TestUpload_rejectsForgedToken, TestUpload_sweepsExpiredPartialAssemblies, TestUpload_sanitizesClientFilename.

Follow the repository's CONVENTIONS.md (test-first, behavioral test names, outside-in through exported API, real over stub over mock). Study the existing upload/file handling (action.go, file.go, and related tests) and reuse existing machinery (session handling, crypto utils, rate limiting) where it exists; where a roadmap dependency is absent in-tree, implement the minimal in-scope equivalent rather than a full subsystem. Keep the diff as small as correctness allows. Do NOT write a browser test.
