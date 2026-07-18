# nullius benchmark sandbox — a controlled, reproducible env for one headless
# `claude` run. Any arm can run inside this so the only
# variable across a campaign is the arm, not the host toolchain/CLI version.
#
# Pins that matter for reproducibility: the claude CLI version and the Go
# toolchain (matched to the task's go.mod `toolchain` directive). Build:
#   docker build -t nullius-bench:latest benchmarks/harness
# Then run the harness with CONTAINER=1 (see run.sh).
FROM node:22-bookworm-slim

# Go toolchain, pinned to the vialite-todo task's `toolchain go1.26.4`.
# GOTOOLCHAIN=local forbids go from silently fetching a different toolchain,
# so the compiler is exactly what this image ships — no network drift.
# gcc + libc6-dev: the Go race detector requires cgo. Without a C compiler
# the agent inside the sandbox cannot run `go test -race` at all (measured:
# a run disclosed "-race was not run: no C compiler", gutting every
# race-forcing exit check while host-side scoring still used -race).
ARG GO_VERSION=1.26.4
RUN apt-get update \
 && apt-get install -y --no-install-recommends git ca-certificates curl gcc libc6-dev \
 && rm -rf /var/lib/apt/lists/* \
 && curl -fsSL "https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz" \
    | tar -C /usr/local -xz
ENV PATH=/usr/local/go/bin:$PATH
ENV GOTOOLCHAIN=local
ENV CGO_ENABLED=1

# Claude Code CLI, pinned to the host version used for the measured campaign.
ARG CLAUDE_VERSION=2.1.212
RUN npm install -g @anthropic-ai/claude-code@${CLAUDE_VERSION}

# The container runs as the invoking uid:gid (see run.sh --user) so files it
# writes into the mounted worktree are host-owned. That uid has no home in the
# image; the harness bind-mounts a writable dir at $HOME. Go caches live under
# it too. These are defaults; run.sh sets HOME explicitly at run time.
ENV GOFLAGS=-mod=mod

WORKDIR /work
