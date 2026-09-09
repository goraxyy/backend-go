# ---- build stage -------------------------------------------------------------
FROM golang:1.22-bookworm AS builder

WORKDIR /src

# Dependencies first so the module cache survives source edits.
COPY src/go.mod src/go.sum ./
RUN go mod download

COPY ./src .
RUN CGO_ENABLED=1 go build -trimpath -o /out/spacesona-go .

# ---- runtime stage -----------------------------------------------------------
# The previous image was a single stage on golang:latest, which shipped the
# whole Go toolchain to production and floated to whatever "latest" happened to
# be. It also ran `apt-get update --allow-unauthenticated`, disabling package
# signature checking, to install a sqlite3 CLI the service never invokes.
FROM debian:bookworm-slim AS runtime

RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app

COPY --from=builder /out/spacesona-go /app/spacesona-go
COPY ./migrations ./migrations
COPY ./firmware ./firmware

# The database file is created at runtime on the mounted volume. The old
# `COPY ./database.db .` referenced a gitignored file that is not in the
# repository, so `docker build` failed outright on any fresh clone.
RUN useradd --system --uid 10001 spaceona \
    && mkdir -p /app/data \
    && chown -R spaceona:spaceona /app
USER spaceona

EXPOSE 3001

CMD ["/app/spacesona-go"]
