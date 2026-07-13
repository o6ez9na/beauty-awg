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
# amneziawg-tools is GPL-2.0: capture the exact corresponding source so it can
# be shipped alongside the redistributed binaries (GPL-2 section 3).
RUN tar -czf /amneziawg-tools-src.tar.gz -C / awg

# ---- stage 3: runtime ----
FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends \
        nftables iproute2 iptables bash openresolv ca-certificates \
    && rm -rf /var/lib/apt/lists/*
COPY --from=awgtools /usr/bin/awg /usr/bin/awg
COPY --from=awgtools /usr/bin/awg-quick /usr/bin/awg-quick
COPY --from=gobuild /out/panel /usr/local/bin/panel

# --- GPL-2.0 compliance for amneziawg-tools (awg, awg-quick) ---------------
# These binaries are GPL-2.0; the license text and the complete corresponding
# source used to build them travel with the image (GPL-2 sections 1 & 3).
COPY --from=awgtools /awg/COPYING /usr/share/doc/amneziawg-tools/COPYING
COPY --from=awgtools /amneziawg-tools-src.tar.gz /usr/share/doc/amneziawg-tools/corresponding-source.tar.gz
COPY <<'EOF' /usr/share/doc/amneziawg-tools/README.source
/usr/bin/awg and /usr/bin/awg-quick are built from amneziawg-tools, licensed
under the GNU General Public License, version 2 (see ./COPYING).

The complete corresponding source used to build them ships in this image at
  ./corresponding-source.tar.gz
and is also publicly available upstream:
  https://github.com/amnezia-vpn/amneziawg-tools

Written offer: for three years from the date you received this image you may
obtain the corresponding source by either means above at no charge.
EOF

# awg config dir (mounted from host so it persists / is visible to host).
RUN mkdir -p /etc/amnezia/amneziawg /etc/awgpanel

EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/panel"]
