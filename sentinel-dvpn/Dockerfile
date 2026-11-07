FROM golang:1.27-alpine3.24 AS build

ARG SENTINEL_NETWORK=mainnet
ARG SENTINEL_PROGRAM_ID=
ARG SENTINEL_MINT=

WORKDIR /src

# If this hangs, the host MTU is likely below 1500 (common on cloud VMs and behind a VPN);
# build with a matching network MTU. See the README, "Docker images".
RUN apk add --no-cache bash git make

COPY . /src

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go mod download && make install SENTINEL_NETWORK=${SENTINEL_NETWORK} SENTINEL_PROGRAM_ID=${SENTINEL_PROGRAM_ID} SENTINEL_MINT=${SENTINEL_MINT}

FROM alpine:3.24

RUN apk add --no-cache ca-certificates

COPY --from=build /go/bin/sentinel-dvpn /usr/local/bin/sentinel-dvpn

ENTRYPOINT ["sentinel-dvpn"]
