# ipadecrypt-toolchain: a Docker image for building the helper C binary on Linux.

ARG DEBIAN_TAG=bookworm-slim
ARG LLVM_VERSION=18
ARG LDID_REV=v2.1.5-procursus7
ARG SDKS_REPO=xybp888/iOS-SDKs
ARG SDKS_COMMIT=dfc736746932759181e879ceae02e12f314c5510
ARG SDK_VERSION=26.4
ARG IOS_DEPLOYMENT_TARGET=14.0


#----- stage: build ldid from source ---------------------------------------
FROM debian:${DEBIAN_TAG} AS ldid-builder
ARG LDID_REV
ENV DEBIAN_FRONTEND=noninteractive
RUN apt-get update && apt-get install -y --no-install-recommends \
        ca-certificates git build-essential pkg-config libssl-dev libplist-dev \
    && rm -rf /var/lib/apt/lists/*
RUN git clone --depth 1 --branch "$LDID_REV" --recurse-submodules \
        https://github.com/ProcursusTeam/ldid /tmp/ldid \
    && make -C /tmp/ldid -j"$(nproc)" \
    && install -Dm 755 /tmp/ldid/ldid /out/ldid


#----- stage: fetch + trim iPhoneOS SDK ------------------------------------
FROM debian:${DEBIAN_TAG} AS sdk-fetcher
ARG SDKS_REPO
ARG SDKS_COMMIT
ARG SDK_VERSION
ENV DEBIAN_FRONTEND=noninteractive
RUN apt-get update && apt-get install -y --no-install-recommends \
        ca-certificates git \
    && rm -rf /var/lib/apt/lists/*
RUN git clone --filter=blob:none --sparse "https://github.com/${SDKS_REPO}" /tmp/sdks \
    && cd /tmp/sdks \
    && git checkout "$SDKS_COMMIT" \
    && git sparse-checkout set "iPhoneOS${SDK_VERSION}.sdk" \
    && rm -rf .git \
    && mv "iPhoneOS${SDK_VERSION}.sdk" /out \
    # Trim to just what this project needs: usr/ (headers + tbd libs) + SDK metadata.
    # Drops System/Library/Frameworks (helper.c uses none) and Developer/.
    # Saves ~256 MB. If a future helper.c needs a framework, revisit this.
    && rm -rf /out/System /out/Developer /out/Entitlements.plist


#----- stage: final runtime image ------------------------------------------
FROM debian:${DEBIAN_TAG}
ARG LLVM_VERSION
ARG SDK_VERSION
ARG IOS_DEPLOYMENT_TARGET

ENV DEBIAN_FRONTEND=noninteractive

# Install clang + lld from apt.llvm.org, ldid runtime deps, then strip the
# install-only tooling back out to keep the image thin.
RUN apt-get update && apt-get install -y --no-install-recommends \
        ca-certificates curl gnupg \
        libssl3 libplist3 \
    && curl -fsSL https://apt.llvm.org/llvm-snapshot.gpg.key \
         | gpg --dearmor -o /usr/share/keyrings/llvm.gpg \
    && echo "deb [signed-by=/usr/share/keyrings/llvm.gpg] http://apt.llvm.org/bookworm/ llvm-toolchain-bookworm-${LLVM_VERSION} main" \
         > /etc/apt/sources.list.d/llvm.list \
    && apt-get update \
    && apt-get install -y --no-install-recommends \
        clang-${LLVM_VERSION} \
        lld-${LLVM_VERSION} \
    # Put unversioned names on PATH and next to clang so -fuse-ld=lld finds ld64.lld.
    && ln -sf /usr/bin/clang-${LLVM_VERSION}    /usr/bin/clang \
    && ln -sf /usr/bin/ld64.lld-${LLVM_VERSION} /usr/bin/ld64.lld \
    && apt-get purge -y curl gnupg \
    && apt-get autoremove -y \
    && rm -rf /var/lib/apt/lists/* \
              /etc/apt/sources.list.d/llvm.list \
              /usr/share/keyrings/llvm.gpg

COPY --from=ldid-builder /out/ldid /usr/local/bin/ldid
COPY --from=sdk-fetcher  /out      /opt/iphoneos.sdk

ENV IPHONEOS_SDK=/opt/iphoneos.sdk \
    IPHONEOS_SDK_VERSION=${SDK_VERSION} \
    IPHONEOS_DEPLOYMENT_TARGET=${IOS_DEPLOYMENT_TARGET}

WORKDIR /workspace
