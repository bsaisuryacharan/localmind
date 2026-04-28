FROM golang:1.22-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /out/localmind-mcp ./cmd/localmind-mcp

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=build /out/localmind-mcp /usr/local/bin/localmind-mcp
EXPOSE 7800
ENV DATA_DIR=/data INDEX_DIR=/var/lib/localmind
ENTRYPOINT ["/usr/local/bin/localmind-mcp"]
