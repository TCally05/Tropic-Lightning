# syntax=docker/dockerfile:1

# --- build stage ---
FROM golang:1.26-bookworm AS build
WORKDIR /src

# Cache dependencies first.
COPY go.mod go.sum ./
RUN go mod download

# Build a static binary (templates are embedded via go:embed, so the runtime
# image needs nothing but the binary).
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/portal ./cmd/portal

# --- runtime stage ---
FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /
COPY --from=build /out/portal /portal

EXPOSE 3000
USER nonroot:nonroot
ENTRYPOINT ["/portal"]
