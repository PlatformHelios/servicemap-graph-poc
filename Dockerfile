# API image: builds the dashboard, embeds it in the Go binary, and serves both.
#
#   podman build -t servicemap-api .

FROM docker.io/library/node:22-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
# vite.config.ts writes to ../internal/httpapi/static, i.e. /src/internal/httpapi/static.
RUN npm run build

FROM docker.io/library/golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY main.go ./
COPY internal/ ./internal/
COPY --from=web /src/internal/httpapi/static/ ./internal/httpapi/static/
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/servicemap .

# distroless/static carries CA certificates (Census geocoder) and tzdata (location hours).
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/servicemap /servicemap
ENV HTTP_ADDR=0.0.0.0:8080
EXPOSE 8080
ENTRYPOINT ["/servicemap"]
CMD ["serve"]
