# ---- build stage ----
FROM golang:1.22 AS builder
WORKDIR /src
COPY . .
# fluent-bit-go has no tagged releases; the pseudo-version pinned in go.mod
# (a commit on its master branch) may no longer be resolvable via
# proxy.golang.org. Drop that require, then re-fetch master directly from
# GitHub and tidy with the proxy bypassed for that module. go-lumber has a
# proper tag so the default proxy is fine for it.
ENV GOFLAGS=-mod=mod
RUN go mod edit -droprequire=github.com/fluent/fluent-bit-go \
 && GOPROXY=direct go get github.com/fluent/fluent-bit-go@master \
 && go get github.com/elastic/go-lumber@latest \
 && GOPROXY=direct go mod tidy
# cgo must be on (it is by default); buildmode c-shared produces the .so
RUN CGO_ENABLED=1 go build -trimpath -buildmode=c-shared -o in_beats.so .

# ---- runtime stage ----
FROM fluent/fluent-bit:latest
COPY --from=builder /src/in_beats.so /fluent-bit/etc/in_beats.so
COPY plugins.conf    /fluent-bit/etc/plugins.conf
COPY fluent-bit.conf /fluent-bit/etc/fluent-bit.conf
EXPOSE 5044
ENTRYPOINT ["/fluent-bit/bin/fluent-bit", "-c", "/fluent-bit/etc/fluent-bit.conf"]
