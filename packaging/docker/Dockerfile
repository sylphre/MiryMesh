# SPDX-License-Identifier: AGPL-3.0-only
# Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.
# MiryMesh Docker — multi-stage, pinned toolchain (synced with go.mod: go 1.26).
# The web console (frontend/, Vue + Vite) is built first; go:embed bakes frontend/dist into the controller.
FROM node:24-bookworm-slim AS frontend
WORKDIR /src/frontend
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY frontend/index.html frontend/vite.config.js ./
COPY frontend/src ./src
RUN npm run build && test -s dist/index.html

FROM golang:1.26-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN rm -rf frontend/dist
COPY --from=frontend /src/frontend/dist ./frontend/dist
ARG VERSION=dev
RUN CGO_ENABLED=1 go build -ldflags="-s -w -X github.com/sylphre/MiryMesh/internal/version.Version=${VERSION}" -o /out/mirymesh-controller ./cmd/controller
RUN CGO_ENABLED=1 go build -ldflags="-s -w -X github.com/sylphre/MiryMesh/internal/version.Version=${VERSION}" -o /out/mirymesh-datanode ./cmd/datanode
RUN CGO_ENABLED=1 go build -ldflags="-s -w -X github.com/sylphre/MiryMesh/internal/version.Version=${VERSION}" -o /out/mirymesh ./cmd/mirymesh

FROM debian:12-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates && rm -rf /var/lib/apt/lists/*
COPY --from=build /out/mirymesh-controller /usr/bin/mirymesh-controller
COPY --from=build /out/mirymesh-datanode /usr/bin/mirymesh-datanode
COPY --from=build /out/mirymesh /usr/bin/mirymesh
EXPOSE 9000
EXPOSE 9001
ENTRYPOINT ["/usr/bin/mirymesh-controller"]