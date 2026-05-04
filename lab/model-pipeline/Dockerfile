FROM golang:1.22-bookworm AS builder

WORKDIR /app

ARG ORT_VERSION=1.20.0

RUN apt-get update && apt-get install -y wget ca-certificates && \
    wget -q https://github.com/microsoft/onnxruntime/releases/download/v${ORT_VERSION}/onnxruntime-linux-x64-${ORT_VERSION}.tgz && \
    tar -xzf onnxruntime-linux-x64-${ORT_VERSION}.tgz && \
    cp onnxruntime-linux-x64-${ORT_VERSION}/lib/libonnxruntime.so* /usr/local/lib/ && \
    cp -r onnxruntime-linux-x64-${ORT_VERSION}/include/* /usr/local/include/ && \
    ldconfig && \
    rm -rf onnxruntime-linux-x64-${ORT_VERSION}* /var/lib/apt/lists/*

COPY go.mod main.go ./
RUN go mod tidy 2>&1

RUN CGO_ENABLED=1 \
    CGO_CFLAGS="-I/usr/local/include" \
    CGO_LDFLAGS="-L/usr/local/lib" \
    go build -o pipeline main.go

FROM debian:bookworm-slim
WORKDIR /app

RUN apt-get update && apt-get install -y ca-certificates && rm -rf /var/lib/apt/lists/*

COPY --from=builder /usr/local/lib/libonnxruntime.so* /usr/local/lib/
COPY --from=builder /app/pipeline .
RUN ldconfig && ln -sf /usr/local/lib/libonnxruntime.so /usr/local/lib/onnxruntime.so

COPY models/ models/
COPY testdata/ testdata/

CMD ["./pipeline"]
