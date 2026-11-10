FROM golang:1.25-alpine AS builder

ARG VERSION=""
ARG REVISION=""

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w -X main.version=${VERSION} -X main.commit=${REVISION}" -o /aiplex-api ./cmd/aiplex-api

FROM gcr.io/distroless/static-debian12:nonroot

ARG VERSION=""
ARG REVISION=""

LABEL org.opencontainers.image.source="https://github.com/vamsiramakrishnan/aiplex"
LABEL org.opencontainers.image.version="${VERSION}"
LABEL org.opencontainers.image.revision="${REVISION}"
LABEL org.opencontainers.image.licenses="Apache-2.0"
LABEL org.opencontainers.image.title="aiplex-api"
LABEL org.opencontainers.image.description="API server for aiplex"

COPY --from=builder /aiplex-api /aiplex-api

USER 65532:65532

EXPOSE 8080
ENTRYPOINT ["/aiplex-api"]
