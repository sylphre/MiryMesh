# SPDX-License-Identifier: AGPL-3.0-only
# Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.
# MiryMesh Docker — Phase4 stub. Multi-stage, pinned Go 1.22.
FROM golang:1.22-bookworm AS build
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
EXPOSE 9000
ENTRYPOINT ["/usr/bin/mirymesh-controller"]
