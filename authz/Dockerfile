FROM rust:1.99.1-alpine AS builder
WORKDIR /app
RUN apk add --no-cache musl-dev
COPY Cargo.toml Cargo.lock ./
COPY src/ src/
RUN cargo build --release --locked

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=builder /app/target/release/aiplex-authz /usr/local/bin/
EXPOSE 9191
ENV PORT=9191
USER nobody
ENTRYPOINT ["aiplex-authz"]
