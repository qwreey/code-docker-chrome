# chrome-front: the bridge between chrome-net and code-docker-internal (see main.go).
FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod main.go page.html ./
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /chrome-front .

FROM alpine:3.22
# /data holds forwards.json (a named volume); owned by the unprivileged user it runs as,
# so the overlay's cap_drop: ALL holds. Forward ports are >= 1024 for the same reason.
RUN mkdir /data && chown 65534:65534 /data
COPY --from=build /chrome-front /usr/local/bin/chrome-front
USER 65534:65534
VOLUME /data
ENTRYPOINT ["/usr/local/bin/chrome-front"]
