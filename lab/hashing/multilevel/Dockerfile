FROM golang:1.24-bookworm

RUN apt-get update && apt-get install -y --no-install-recommends \
    pkg-config \
    libopencv-dev \
    curl \
    ca-certificates \
    && rm -rf /var/lib/apt/lists/*

# Install ONNX Runtime
RUN curl -L https://github.com/microsoft/onnxruntime/releases/download/v1.20.1/onnxruntime-linux-x64-1.20.1.tgz -o onnxruntime.tgz \
    && tar -xzf onnxruntime.tgz \
    && cp onnxruntime-linux-x64-1.20.1/lib/libonnxruntime.so* /usr/lib/ \
    && rm -rf onnxruntime.tgz onnxruntime-linux-x64-1.20.1

WORKDIR /app

# Copy go.mod and go.sum first for better caching
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Download dependencies
RUN go mod tidy

ENV LD_LIBRARY_PATH=/usr/lib
# ArUco module not needed — skip to avoid requiring opencv_contrib headers

CMD ["go", "run", "-tags", "gocv_specific_modules", "main.go"]
