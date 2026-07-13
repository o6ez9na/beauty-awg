# syntax=docker/dockerfile:1

# ---- stage 1: build the Go panel ----
FROM golang:1.26-bookworm AS gobuild
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/panel ./cmd/panel

# ---- stage 2: build amneziawg-tools (awg, awg-quick) ----
FROM debian:bookworm AS awgtools
RUN apt-get update && apt-get install -y --no-install-recommends \
        git build-essential libmnl-dev ca-certificates \
    && rm -rf /var/lib/apt/lists/*
ARG AWG_TOOLS_REF=master
RUN git clone --depth 1 --branch "$AWG_TOOLS_REF" \
        https://github.com/amnezia-vpn/amneziawg-tools.git /awg
RUN make -C /awg/src && make -C /awg/src install
# installs /usr/bin/awg and /usr/bin/awg-quick

# ---- stage 3: runtime ----
FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends \
        nftables iproute2 iptables bash openresolv ca-certificates \
    && rm -rf /var/lib/apt/lists/*
COPY --from=awgtools /usr/bin/awg /usr/bin/awg
COPY --from=awgtools /usr/bin/awg-quick /usr/bin/awg-quick
COPY --from=gobuild /out/panel /usr/local/bin/panel

# awg config dir (mounted from host so it persists / is visible to host).
RUN mkdir -p /etc/amnezia/amneziawg /etc/awgpanel

EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/panel"]
