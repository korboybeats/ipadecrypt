#!/bin/sh
# Created by IPA Decryption bot
# Build a static Unicorn (aarch64 only) for iOS and print its path.
#
# The appstore-helper links this archive with -force_load/-export_dynamic so
# internal/sap/unicorn finds uc_* through dlsym(RTLD_DEFAULT). Unicorn's TCG
# buffer is patched to stay W^X and flip with mprotect (app/unicorn-ios), and
# the process marks itself CS_DEBUGGED via Dopamine's jbctl, which is what lets
# generated code run on iOS without MAP_JIT/pthread_jit_write_protect_np.
#
# Requires macOS with Xcode (iphoneos SDK), cmake and curl. Output is cached in
# app/.unicorn-ios/ (gitignored); delete it to force a rebuild.

set -e
cd "$(dirname "$0")"
APP_DIR=$(pwd)

UC_VERSION=2.1.4
UC_SHA256=ea8863f095a0136388694e5a6063afd9bb7650e30243dd6251af59c5ce5601f4
OUT_DIR="$APP_DIR/.unicorn-ios"
OUT_LIB="$OUT_DIR/libunicorn-ios.a"
STAMP="$OUT_DIR/stamp"
WANT_STAMP="$UC_VERSION $(cat unicorn-ios/ipd_ios_jit.h unicorn-ios/ipd_ios_jit.c build_unicorn_ios.sh | shasum -a 256 | cut -d' ' -f1)"

if [ -f "$OUT_LIB" ] && [ "$(cat "$STAMP" 2>/dev/null)" = "$WANT_STAMP" ]; then
  echo "$OUT_LIB"
  exit 0
fi

command -v xcrun >/dev/null 2>&1 || { echo "build_unicorn_ios.sh needs macOS/Xcode" >&2; exit 1; }
command -v cmake >/dev/null 2>&1 || { echo "build_unicorn_ios.sh needs cmake" >&2; exit 1; }

SDK=$(xcrun --sdk iphoneos --show-sdk-path)
WORK="$OUT_DIR/work"
rm -rf "$WORK"
mkdir -p "$WORK"

TARBALL="$OUT_DIR/unicorn-$UC_VERSION.tar.gz"
if [ ! -f "$TARBALL" ]; then
  curl -fsSL -o "$TARBALL.part" "https://github.com/unicorn-engine/unicorn/archive/refs/tags/$UC_VERSION.tar.gz"
  mv "$TARBALL.part" "$TARBALL"
fi
echo "$UC_SHA256  $TARBALL" | shasum -a 256 -c - >&2
tar -xzf "$TARBALL" -C "$WORK"
SRC="$WORK/unicorn-$UC_VERSION"

# Keep the TCG buffer W^X: map it RW (no MAP_JIT) and register it with the shim.
python3 - "$SRC/qemu/accel/tcg/translate-all.c" <<'PY'
import sys
p = sys.argv[1]
s = open(p).read()
old_alloc = """    int prot = PROT_WRITE | PROT_READ | PROT_EXEC;
    int flags = MAP_PRIVATE | MAP_ANONYMOUS;
    size_t size = tcg_ctx->code_gen_buffer_size;
    void *buf;
#ifdef USE_MAP_JIT
    flags |= MAP_JIT;
#endif
    buf = mmap(NULL, size, prot, flags, -1, 0);
    if (buf == MAP_FAILED) {
        return NULL;
    }
"""
new_alloc = """    int prot = PROT_WRITE | PROT_READ;
    int flags = MAP_PRIVATE | MAP_ANONYMOUS;
    size_t size = tcg_ctx->code_gen_buffer_size;
    void *buf;
    buf = mmap(NULL, size, prot, flags, -1, 0);
    if (buf == MAP_FAILED) {
        return NULL;
    }
    if (ipd_jit_register(buf, size) != 0) {
        munmap(buf, size);
        return NULL;
    }
"""
old_free = """    if (tcg_ctx->initial_buffer) {
        if (munmap(tcg_ctx->initial_buffer, tcg_ctx->initial_buffer_size)) {"""
new_free = """    if (tcg_ctx->initial_buffer) {
        ipd_jit_unregister(tcg_ctx->initial_buffer);
        if (munmap(tcg_ctx->initial_buffer, tcg_ctx->initial_buffer_size)) {"""
for old, new in ((old_alloc, new_alloc), (old_free, new_free)):
    if s.count(old) != 1:
        sys.exit("translate-all.c patch anchor not found; Unicorn source changed")
    s = s.replace(old, new)
open(p, "w").write(s)
PY

CCWRAP="$WORK/ioscc"
cat > "$CCWRAP" <<CC
#!/bin/sh
exec xcrun -sdk iphoneos clang -target arm64-apple-ios15.0 -include "$APP_DIR/unicorn-ios/ipd_ios_jit.h" "\$@"
CC
chmod +x "$CCWRAP"

mkdir -p "$WORK/build"
( cd "$WORK/build" && \
  ARCHFLAGS="-arch arm64" cmake "$SRC" \
    -DCMAKE_SYSTEM_NAME=iOS -DCMAKE_OSX_ARCHITECTURES=arm64 \
    -DCMAKE_OSX_DEPLOYMENT_TARGET=15.0 -DCMAKE_OSX_SYSROOT="$SDK" \
    -DCMAKE_C_COMPILER="$CCWRAP" -DUNICORN_ARCH=aarch64 \
    -DBUILD_SHARED_LIBS=OFF -DUNICORN_LEGACY_STATIC_ARCHIVE=ON \
    -DUNICORN_BUILD_TESTS=OFF -DUNICORN_INSTALL=OFF \
    -DCMAKE_BUILD_TYPE=Release >&2 && \
  make -j"$(sysctl -n hw.ncpu)" unicorn >&2 )

"$CCWRAP" -O2 -c "$APP_DIR/unicorn-ios/ipd_ios_jit.c" -o "$WORK/build/ipd_ios_jit.o"
libtool -static -o "$OUT_LIB.tmp" \
  "$WORK/build/libunicorn-static.a" "$WORK/build/libunicorn-common.a" \
  "$WORK/build/libaarch64-softmmu.a" "$WORK/build/ipd_ios_jit.o" 2>/dev/null
mv "$OUT_LIB.tmp" "$OUT_LIB"
echo "$WANT_STAMP" > "$STAMP"
rm -rf "$WORK"
echo "$OUT_LIB"
