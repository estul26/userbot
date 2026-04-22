# syntax=docker/dockerfile:1

FROM golang:1.25-alpine AS builder

WORKDIR /app
RUN apk add --no-cache git

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /bin/app ./cmd/bot

FROM gcr.io/distroless/static-debian12
WORKDIR /app

COPY --from=builder /bin/app /app/app

USER nonroot:nonroot

ENTRYPOINT ["/app/app"]
