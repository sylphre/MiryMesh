# SPDX-License-Identifier: AGPL-3.0-only
# Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.
# MiryMesh Docker — multi-stage, pinned toolchain (synced with go.mod: go 1.26).
FROM golang:1.26-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /out/mirymesh-controller ./cmd/controller
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /out/mirymesh-datanode ./cmd/datanode
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /out/mirymesh ./cmd/mirymesh

FROM gcr.io/distroless/static-debian12
COPY --from=build /out/mirymesh-controller /usr/bin/mirymesh-controller
COPY --from=build /out/mirymesh-datanode /usr/bin/mirymesh-datanode
COPY --from=build /out/mirymesh /usr/bin/mirymesh
COPY web/static /usr/share/mirymesh/static
EXPOSE 9000
EXPOSE 9001
ENTRYPOINT ["/usr/bin/mirymesh-controller"]
