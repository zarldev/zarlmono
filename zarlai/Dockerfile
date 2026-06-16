# syntax=docker/dockerfile:1.7

# ---- Stage 1: build the React frontend
FROM node:24-bookworm-slim AS frontend
WORKDIR /work
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci
COPY frontend/ ./
RUN npm run build

# ---- Stage 2: Go build with CGo against dlib + cblas + libjpeg
# Debian ships first-class libdlib-dev / libatlas-base-dev (cblas) /
# libjpeg-dev — the same set CI uses for lint + test. Chainguard's
# free wolfi index does not publish dlib, so we stay on Debian here.
FROM golang:1.26-bookworm AS go-build
RUN apt-get update && apt-get install -y --no-install-recommends \
        build-essential \
        libdlib-dev \
        libatlas-base-dev \
        libjpeg-dev \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=frontend /work/dist ./frontend/dist
ENV CGO_ENABLED=1
RUN go build -trimpath -ldflags="-s -w" -o /out/zarl ./cmd/zarl

# ---- Stage 3: slim Debian runtime with the matching shared libs
FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends \
        libdlib19.1 \
        libatlas3-base \
        libjpeg62-turbo \
        ca-certificates \
    && rm -rf /var/lib/apt/lists/* \
    && useradd --system --uid 65532 --no-create-home --shell /usr/sbin/nologin nonroot
USER nonroot
WORKDIR /app
COPY --from=go-build --chown=nonroot:nonroot /out/zarl /usr/local/bin/zarl

# Models, .env, Dolt/Qdrant/SearXNG/llama-server are operator-supplied
# at runtime. The container exposes :8080 only.
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/zarl"]
