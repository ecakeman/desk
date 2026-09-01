FROM mcr.microsoft.com/devcontainers/go:1.25-bookworm

ARG APT_MIRROR=https://mirrors.aliyun.com
ARG NODE_MIRROR=https://npmmirror.com/mirrors/node
ARG NODE_VERSION=22.14.0
ARG NPM_REGISTRY=https://registry.npmmirror.com
ARG PNPM_VERSION=10.6.5
ARG GOPROXY=https://goproxy.cn,direct

RUN if [ -f /etc/apt/sources.list.d/debian.sources ]; then \
        sed -i \
          -e "s|https\?://security.debian.org|${APT_MIRROR}|g" \
          -e "s|https\?://deb.debian.org|${APT_MIRROR}|g" \
          /etc/apt/sources.list.d/debian.sources; \
    fi \
    && if [ -f /etc/apt/sources.list ]; then \
        sed -i \
          -e "s|https\?://security.debian.org|${APT_MIRROR}|g" \
          -e "s|https\?://deb.debian.org|${APT_MIRROR}|g" \
          /etc/apt/sources.list; \
    fi \
    && apt-get update \
    && apt-get install -y --no-install-recommends \
        ca-certificates \
        curl \
        make \
        python3 \
        python3-venv \
        xz-utils \
    && rm -rf /var/lib/apt/lists/*

RUN arch="$(dpkg --print-architecture)" \
    && case "$arch" in \
         amd64) node_arch=x64 ;; \
         arm64) node_arch=arm64 ;; \
         *) echo "unsupported architecture: $arch" >&2; exit 1 ;; \
       esac \
    && curl -fsSL \
       "${NODE_MIRROR}/v${NODE_VERSION}/node-v${NODE_VERSION}-linux-${node_arch}.tar.xz" \
       -o /tmp/node.tar.xz \
    && tar -xJf /tmp/node.tar.xz -C /usr/local --strip-components=1 \
    && rm /tmp/node.tar.xz \
    && npm config set --global registry "${NPM_REGISTRY}" \
    && npm install --global "pnpm@${PNPM_VERSION}" \
    && npm cache clean --force

ENV GOPROXY=${GOPROXY}
ENV NPM_CONFIG_REGISTRY=${NPM_REGISTRY}
