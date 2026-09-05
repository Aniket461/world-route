# Optional root Dockerfile that builds only the API (Railway uses backend/Dockerfile).
# Prefer: docker compose up --build
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/*.go ./
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/world-route .

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/world-route /world-route
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
ENV PORT=8080
EXPOSE 8080
USER nonroot:nonroot
CMD ["/world-route"]
