# syntax=docker/dockerfile:1

FROM golang:1.25-alpine AS builder

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/job-scout .

FROM debian:bookworm-slim

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
       ca-certificates \
       pandoc \
       python3-weasyprint \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app

COPY --from=builder /out/job-scout ./job-scout

RUN mkdir -p /app/data/resumes

EXPOSE 8080

ENTRYPOINT ["./job-scout"]
