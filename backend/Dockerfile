# Build the Go API for Railway / any container host.
FROM golang:1.26-alpine AS build
WORKDIR /src
RUN apk add --no-cache ca-certificates
COPY go.mod go.sum ./
RUN go mod download
COPY *.go ./
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/world-route .

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /
COPY --from=build /out/world-route /world-route
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
ENV PORT=8080
EXPOSE 8080
USER nonroot:nonroot
CMD ["/world-route"]
