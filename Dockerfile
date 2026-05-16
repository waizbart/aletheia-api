FROM golang:1.24-bookworm AS builder

RUN apt-get update && apt-get install -y --no-install-recommends \
    pkg-config \
    libopencv-dev \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=1 go build -o /bin/aletheia-api ./cmd/api

FROM debian:bookworm-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    tzdata \
    libopencv-calib3d406 \
    libopencv-core406 \
    libopencv-dnn406 \
    libopencv-features2d406 \
    libopencv-flann406 \
    libopencv-highgui406 \
    libopencv-imgcodecs406 \
    libopencv-imgproc406 \
    libopencv-ml406 \
    libopencv-objdetect406 \
    libopencv-photo406 \
    libopencv-video406 \
    libopencv-videoio406 \
    && rm -rf /var/lib/apt/lists/*

COPY --from=builder /bin/aletheia-api /bin/aletheia-api
COPY migrations /migrations

EXPOSE 8080

ENTRYPOINT ["/bin/aletheia-api"]
