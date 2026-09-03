FROM --platform=linux/amd64 golang:1.24-bookworm AS build

ARG JUNO_BROADCAST_REPO=https://github.com/Abdullah1738/juno-broadcast.git
ARG JUNO_BROADCAST_REF=76689b4d23bd27d0947f91aa7abccf8075c9782f

RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    git \
  && rm -rf /var/lib/apt/lists/*

WORKDIR /src
RUN git clone "${JUNO_BROADCAST_REPO}" .
RUN git checkout "${JUNO_BROADCAST_REF}"

RUN go mod download
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/juno-broadcast ./cmd/juno-broadcast

FROM --platform=linux/amd64 debian:bookworm-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    curl \
  && rm -rf /var/lib/apt/lists/*

COPY --from=build /out/juno-broadcast /usr/local/bin/juno-broadcast

ENTRYPOINT ["juno-broadcast"]
