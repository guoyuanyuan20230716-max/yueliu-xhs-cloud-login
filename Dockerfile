FROM golang:1.24 AS builder
WORKDIR /src
# Pin the same upstream revision that was validated on the initial Render service.
RUN git init . && git remote add origin https://github.com/xpzouying/xiaohongshu-mcp.git && \
    git fetch --depth=1 origin 7797fd375aba02dcd33afe143f6bee13e5870859 && git checkout --detach FETCH_HEAD
COPY login_console.go login_console.html login_console_test.go reader.go reader.html reader_test.go ./
RUN sed -i '/router.GET("\/health", healthHandler)/a\ registerLoginConsole(router, appServer)' routes.go && \
    gofmt -w login_console.go login_console_test.go reader.go reader_test.go routes.go && \
    go test . -run '^TestConsole' -count=1 && \
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags='-s -w -X main.version=yueliu-login-1' -o /out/app .

FROM ubuntu:22.04
ENV DEBIAN_FRONTEND=noninteractive TZ=Asia/Shanghai
WORKDIR /app
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates curl fonts-liberation fonts-noto-color-emoji fonts-unifont fonts-freefont-ttf fonts-wqy-zenhei \
    libasound2 libatk-bridge2.0-0 libatk1.0-0 libc6 libcairo2 libcairo-gobject2 libcups2 libdbus-1-3 \
    libdrm2 libexpat1 libfontconfig1 libgdk-pixbuf-2.0-0 libgbm1 libgcc1 libglib2.0-0 libgtk-3-0 \
    libnspr4 libnss3 libpango-1.0-0 libpangocairo-1.0-0 libstdc++6 libx11-6 libx11-xcb1 libxcb1 \
    libxcomposite1 libxcursor1 libxdamage1 libxext6 libxfixes3 libxi6 libxkbcommon0 libxrandr2 \
    libxrender1 libxshmfence1 libxss1 libxtst6 lsb-release tini wget xdg-utils xz-utils && \
    rm -rf /var/lib/apt/lists/* && mkdir -p /app/data/home /app/data/config /app/images
ENV XDG_CACHE_HOME=/app/cache HOME=/app/data/home XDG_CONFIG_HOME=/app/data/config COOKIES_PATH=/app/data/cookies.json
COPY --from=builder /src/browser/browser_version.txt /tmp/browser_version.txt
RUN VER="$(tr -d '[:space:]' < /tmp/browser_version.txt)" && \
    BASE="https://cdn.one-world.ai/browsers/${VER}" && \
    BROWSER_DIR="${XDG_CACHE_HOME}/xiaohongshu-mcp/browser/${VER}" && mkdir -p "${BROWSER_DIR}" && \
    curl --retry 3 --connect-timeout 30 -fsSL -o /tmp/browser.tar.xz "${BASE}/linux-x64.tar.xz" && \
    curl --retry 3 --connect-timeout 30 -fsSL "${BASE}/SHA256SUMS" | grep ' linux-x64.tar.xz$' | awk '{print $1"  /tmp/browser.tar.xz"}' | sha256sum -c - && \
    tar -xJf /tmp/browser.tar.xz -C "${BROWSER_DIR}" --strip-components=1 && \
    rm /tmp/browser.tar.xz /tmp/browser_version.txt && test -x "${BROWSER_DIR}/chrome"
COPY --from=builder /out/app /app/app
EXPOSE 18060
ENTRYPOINT ["/usr/bin/tini", "-s", "--"]
CMD ["./app"]
