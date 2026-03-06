FROM --platform=linux/amd64 rust:1.86-bookworm AS rust-build

ARG JUNO_SCAN_REPO=https://github.com/junocash-tools/juno-scan.git
ARG JUNO_SCAN_REF=8e40d26577be1e946823ab3f380be5baf8a1dccd

RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    git \
    pkg-config \
  && rm -rf /var/lib/apt/lists/*

WORKDIR /src
RUN git clone "${JUNO_SCAN_REPO}" .
RUN git checkout "${JUNO_SCAN_REF}"

RUN cargo build --release --manifest-path rust/scan/Cargo.toml

FROM --platform=linux/amd64 golang:1.24-bookworm AS go-build

RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    build-essential \
  && rm -rf /var/lib/apt/lists/*

WORKDIR /src
COPY --from=rust-build /src /src

RUN go mod download
RUN go build -trimpath -o /out/juno-scan ./cmd/juno-scan

FROM --platform=linux/amd64 debian:bookworm-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    curl \
  && rm -rf /var/lib/apt/lists/*

COPY --from=go-build /out/juno-scan /usr/local/bin/juno-scan
COPY --from=go-build /src/rust/scan/target/release/libjuno_scan.so /usr/local/lib/libjuno_scan.so

ENV LD_LIBRARY_PATH=/usr/local/lib

ENTRYPOINT ["juno-scan"]
