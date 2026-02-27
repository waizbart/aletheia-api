FROM golang:1.24-alpine3.21 AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -o /bin/aletheia-api ./cmd/api

FROM alpine:3.21

RUN apk add --no-cache ca-certificates tzdata

COPY --from=builder /bin/aletheia-api /bin/aletheia-api
COPY migrations /migrations

EXPOSE 8080

ENTRYPOINT ["/bin/aletheia-api"]
